## 2. Harden the Foundation

*Correctness under failure is a feature you ship before the failure arrives.*

## The Situation

The API talked to the rest of the system over request-reply channels, and every handler trusted that conversation to end. A handler built a reply channel, pushed a `models.Request` into the service's inbox, then parked on `resp := <-replyCh` with nothing else happening. If the entity service was down or wedged, the handler blocked forever: no timeout, no cancellation, no response, and a goroutine pinned for the life of the process. Errors were worse than absent, they were lying. Every failure funneled into `respondError(c, http.StatusInternalServerError, resp.Error.Error())`, so a missing record, a duplicate key, and a dead database all came back as 500 with the raw driver string pasted into the body. Validation lived in `binding:"required"` struct tags that only meant anything inside gin's `ShouldBindJSON`; the moment the transport changed, the guarantees evaporated. Startup was quietly permissive too: `ValidateSecrets` emitted a warning, and the server was glad to serve JWTs, encryption keys, and admin hashes over plain HTTP in production.

## The Transformation

The first fix gave every channel crossing a deadline. The old handler was three trusting lines: push, wait, check. The new one routes the whole conversation through `doRequest`, which selects on the request context and a five-second `rpcTimeout` on each send and reply, returning `errServiceUnavailable` instead of hanging.

**BEFORE** — `pkg/api/routes.go` (0e7d5ef^)

```go
replyCh := make(chan models.Response, 1)
reqCh <- models.Request{
    Operation:  models.OpList,
    EntityType: entityType,
    ReplyCh:    replyCh,
}
resp := <-replyCh
if resp.Error != nil {
    respondError(c, http.StatusInternalServerError, resp.Error.Error())
    return
}
```

**AFTER** — `pkg/api/routes.go` (0e7d5ef)

```go
func doRequest(r *http.Request, reqCh chan<- models.Request, req models.Request) (models.Response, error) {
    ctx := r.Context()
    select {
    case reqCh <- req:
    case <-ctx.Done():
        return models.Response{}, ctx.Err()
    case <-time.After(rpcTimeout):
        return models.Response{}, errServiceUnavailable
    }
    select {
    case resp := <-req.ReplyCh:
        return resp, nil
    case <-ctx.Done():
        return models.Response{}, ctx.Err()
    case <-time.After(rpcTimeout):
        return models.Response{}, errServiceUnavailable
    }
}
```

The before version had an unbounded wait, so a dead service turned a health check into a goroutine leak. The after version bounds the wait at every hop, honors client disconnects via `ctx.Done()`, and keeps the reply channel buffered so the service side never blocks on a handler that already gave up. `respondRPCError` maps the sentinel to a real 503 instead of a lie.

The second fix made errors honest. `classifyError` replaced the one-size-fits-all 500 with a switch that reads the actual failure: `sql.ErrNoRows` and `record not found` become 404, duplicate keys and the Postgres `23503` foreign-key violation become 409, validation phrases become 400, and everything else becomes a bare `internal server error`.

**BEFORE** — `pkg/api/routes.go` (0e7d5ef^, createHandler)

```go
resp := <-replyCh
if resp.Error != nil {
    respondError(c, http.StatusInternalServerError, resp.Error.Error())
    return
}
```

**AFTER** — `pkg/api/routes.go` (0e7d5ef)

```go
func respondServiceError(w http.ResponseWriter, operation, entityType string, err error) {
    slog.Error("API request failed", "operation", operation, "entity_type", entityType, "error", err)
    code, msg := classifyError(err)
    respondError(w, code, msg)
}
```

Sending `resp.Error.Error()` to the client handed strangers the inside of your database layer and made every failure indistinguishable. The after version logs the full error server-side and forwards only a classified, client-safe message, so a client can act on the response while DB internals stay in the logs.

The third fix moved validation out of the framework and into the service. The models carried `binding:"omitempty,min=1,max=65535"` tags that only gin honored; the same checks now live in `EntityService` as plain functions run on every create and update, transport be damned.

**BEFORE** — `pkg/models/models.go` (0e7d5ef^)

```go
Port                   int    `db:"port" json:"port" binding:"omitempty,min=1,max=65535" update:"omitempty"`
Status                 string `db:"status" json:"status" binding:"omitempty,oneof=discovered active inactive error" update:"omitempty"`
```

**AFTER** — `pkg/Services/persistence/entityService.go` (0e7d5ef)

```go
func validateDeviceFields(device *models.Device) error {
    if device.Port != 0 && (device.Port < 1 || device.Port > 65535) {
        return fmt.Errorf("port must be between 1 and 65535")
    }
    if device.PollingIntervalSeconds != 0 && (device.PollingIntervalSeconds < 60 || device.PollingIntervalSeconds > 3600) {
        return fmt.Errorf("polling_interval_seconds must be between 60 and 3600")
    }
    if device.IPAddress != "" && net.ParseIP(device.IPAddress) == nil {
        return fmt.Errorf("ip_address must be a valid IP address")
    }
    switch device.Status {
    case "", "discovered", "active", "inactive", "error":
    default:
        return fmt.Errorf("invalid status %q", device.Status)
    }
    return nil
}
```

Tags in the model only worked inside gin, so validation was a transport accident. As service functions, the same rules run no matter how a request arrives, and their error strings are exactly the phrases `classifyError` recognizes, so the two halves agree on what a 400 looks like.

The same spirit hit startup and the server. `ValidateSecrets` became fail-fast: in production, a default key or admin hash means `os.Exit(1)`, and `main` refuses to start at all without TLS configured, because this API carries JWTs, encryption keys, and admin hashes. The duplicated TLS and plain-HTTP branches collapsed into one `newHTTPServer` with explicit timeouts, `ReadHeaderTimeout` of five seconds and `WriteTimeout` of thirty, so slow clients cannot hold connections open. A new `MaxBodyBytes` middleware capped request bodies at 1 MiB. And for the first time the commit shipped a real test suite, 308 lines of `api_test.go` on `httptest` including `TestDoRequestTimesOutWhenServiceNeverReplies`, plus pool, service, and plugin tests, so the bounded waits and classified errors were proven instead of hoped for.

## The Lesson

**Assume every partner in the system can fail, and make each failure bounded, honest, and local.** A timeout beats a wedged goroutine, a 404 beats a raw driver string, validation at the service boundary outlives any framework, and a process that refuses to boot unsafe is safer than one that runs with a warning. Tests turn these intentions into guarantees.
