# NMS Codebase Audit

Date: 2026-07-31
Scope: `nms/` Go module (cmd, pkg, plugin-code). Build `go build ./...` and `go vet ./...` pass. All existing tests pass.

Test coverage is low where it matters most: **scheduler and poller have 0% coverage**, and the persistence/entity service that drives scheduling has 1.8%. The concurrency-heavy core (scheduler + poller) is entirely untested.

---

## High severity

### H1. Duplicate deadline-queue entries cause a device to be polled multiple times per interval
`pkg/Services/scheduling/monitorScheduler.go` + `pkg/Services/persistence/entityService.go`

Every `EventCreate`/`EventUpdate` calls `sched.queue.PushEntry(...)` (scheduler `processDeviceEvent`, lines 101/106), and `schedule()` re-queues each device every tick. `handleGetBatch` (entityService line 572) iterates `req.IDs` with **no de-duplication**, so if a device has N expired queue entries it is returned N times in `ToPing`/`ToSkip` and polled N times.

This is observable in `app.log`: device 3 is "Device qualified (ping skipped)" twice within the same tick and appears twice in a `Dispatching qualified devices count:2` batch (for a single device).

Why it happens: on startup `InitQueue` adds one entry; then provisioning/deactivation/status events each add a fresh immediate-deadline entry; the old entry is never removed (lazy deletion). Repeated events multiply entries.

Impact: duplicate/over-polling, wasted WinRM connections and DB writes, and eventual unbounded queue growth for frequently-updated devices.

Fix options:
- De-duplicate in `handleGetBatch` (keep a `seen` set over `req.IDs`), and/or
- Maintain a `map[int64]time.Time` of the latest deadline per device instead of allowing duplicate heap entries (true O(log n) upsert, replaces lazy deletion).

### H2. A `/16` discovery is allowed by the core but rejected by the plugin — silently fails
`pkg/Services/discovery/discoveryService.go` (`MaxExpandHosts = 65536`, line 23) vs `plugin-code/winrm/main.go` (`maxInputsPerBatch = 10000`, line 27).

`runDiscovery` submits up to 65,534 hosts as **one** pool job (one plugin process). The winrm plugin exits with an error if the input batch exceeds 10,000, so the entire `/16` discovery silently produces no devices.

Either lower `MaxExpandHosts` to match the plugin cap, chunk pool jobs, or raise `maxInputsPerBatch` consistently. This mismatch should be an explicit invariant, not two independent constants.

### H3. Scheduler can block indefinitely on non-context-aware sends
`pkg/Services/scheduling/monitorScheduler.go` lines 139, 192, 226:

```go
sched.entityReqChan <- models.Request{...}   // 139
sched.FailureChan <- models.Event{...}       // 192
sched.OutputChan <- qualified                 // 226
```

These are raw blocking sends. Every other service was hardened with `select { case <-ctx.Done(): ... }` guards (poller, worker pool, metrics, health monitor), but the scheduler was not. If a consumer (HealthMonitor, Poller) exits first during shutdown, a mid-`schedule()` scheduler blocks forever. The process still exits because `main` returns, but "graceful shutdown complete" is logged while goroutines are still wedged, and in a future refactor that drains services it would hang. Use `models.Call`/ctx-guarded sends consistently here.

---

## Medium severity

### M1. Credential update path can destroy the encrypted payload
`pkg/api/routes.go` `updateHandler` + `pkg/models/models.go` (`Payload ... binding:"required"`).

The API GET/list responses **mask** `payload` as `"[HIDDEN]"` (`maskCredentialPayload`). On PUT, `ShouldBindJSON` requires `payload` (non-zero). A client doing read-modify-write sends `"[HIDDEN]"`, which gets AES-encrypted and stored, corrupting the credential. There is also no way to do a partial update that preserves the stored ciphertext.

Recommend: make `payload` optional on update and skip encryption when omitted/`[HIDDEN]`; only re-encrypt when the client supplies real plaintext.

### M2. `ValidateSecrets` does not reject the default admin credential
`pkg/config/config.go` only checks `JWT_SECRET` and `ENCRYPTION_KEY`. The default `NMS_ADMIN_HASH` is the public bcrypt hash of `admin` (well-known), and `start.sh` defaults to `admin/admin`. In `APP_ENV=production`, the app still starts with default admin credentials. Add the default admin hash (and empty `DB_PASSWORD`) to `ValidateSecrets`.

### M3. SQL Injection in pgx + other vulnerable dependencies
`govulncheck` reports 6 reachable vulnerabilities:
- **GO-2026-5004 (pgx v5.7.6 → fixed v5.9.2): SQL injection via placeholder confusion with dollar-quoted string literals.** Reachable via `database.ConnectRaw`/`sanitize.SanitizeSQL`. The metrics queries are parameterized, so this is likely not directly exploitable here, but upgrade `github.com/jackc/pgx/v5`.
- Stdlib (`go1.26.3`) issues in `net/textproto` (GO-2026-5039), `crypto/x509` (GO-2026-5037), `quic-go`, `http3` (GO-2026-5039/5044). Upgrade the toolchain.

### M4. HTTP (non-TLS) mode ships credentials and JWTs in cleartext
`cmd/app/main.go` runs plain HTTP on :8080 when `TLS_CERT_FILE`/`TLS_KEY_FILE` are unset (the default). The winrm plugin defaults to TLS (good), but the management API — which handles `ENCRYPTION_KEY`, `NMS_ADMIN_HASH`, and JWTs — has no enforcement. For production, require TLS (e.g., fail startup in production without a cert) and add `Strict-Transport-Security` when HTTPS.

### M5. Login throttle can be bypassed/locked by spoofed `X-Forwarded-For`
`pkg/api/jwtAuth.go` uses `context.ClientIP()` for the throttle key, and the app never calls `SetTrustedProxies`. Gin's default trusts all proxies, so a client that can reach the app directly can spoof `X-Forwarded-For` to either bypass their own rate limit or lock out other users. Set trusted proxies explicitly (or use a non-spoofable identity) before relying on the throttle in production.

### M6. `cacheBackfill` is dead code — caches only reconcile at startup
`pkg/Services/persistence/entityService.go` lines 640-656 define `cacheBackfill` but it is never started in `main.go`. The in-memory device/credential caches are loaded once at startup; devices added/removed by direct DB writes (not through the API) are never reconciled. Either wire `cacheBackfill` into the service loop or remove the dead code.

---

## Low severity / hygiene

- **L1 — Foreign-key violations surface as 500.** Deleting a `credential_profiles` row still referenced by devices hits SQLSTATE 23503, which `classifyError` (`pkg/api/routes.go`) doesn't map (only 23505/duplicate). Return 409 Conflict instead of a generic 500.
- **L2 — Unnecessary goroutine-per-event.** `entityService` wraps the already-non-blocking `sendEvent` in `go sendEvent(...)` at ~10 call sites. Redundant and spawns unbounded goroutines; call `sendEvent` directly.
- **L3 — Security headers incomplete.** `SecurityHeaders` (`pkg/api/jwtAuth.go`) sets `X-XSS-Protection` (deprecated and can weaken security) but lacks `Content-Security-Policy`, `Referrer-Policy`, `Permissions-Policy`, and `Strict-Transport-Security`. Consider dropping `X-XSS-Protection` and adding the others.
- **L4 — `DecryptPayload` silently accepts plaintext credentials in production.** `pkg/api/encryption.go` falls back to raw JSON for any payload starting with `{`. This masks real key-rotation/corruption failures and would accept plaintext creds stored by an operator. Gate this fallback to non-production or fail closed.
- **L5 — README/Go version drift.** `go.mod` declares `go 1.25.5`; README says Go 1.21+. Update docs.
- **L6 — WinRM connection fan-out.** Pool workers each run a batch that the plugin parallelizes up to `maxConcurrent=32`. With `POLL_WORKER_COUNT=10`, up to 320 concurrent WinRM sessions. Fine if sized deliberately, but make the total bound explicit/observable.
- **L7 — Build artifacts committed.** `winrm-pin` appears untracked under `plugin-code/winrm/`; compiled `plugins/winrm` and `bin/` may be committed. Add to `.gitignore`.

---

## Test coverage gaps

| Package | Coverage | Notes |
|---|---|---|
| `scheduling` | 0% | No tests at all. Priority queue + scheduler are the correctness-critical core. |
| `polling` | 0% | No tests. Task/credential flow, grouping. |
| `persistence` | 1.8% | Only helper functions tested; no `EntityService` scheduling/cache/CRUD tests. |
| `config`, `models`, `cmd/app` | 0% | No tests. |
| `discovery` | 39.5% | Target-expansion well tested; pending/result flow not. |
| `api` | 35.2% | Good coverage of auth/throttle/RPC error paths. |
| `pluginWorker` | 85.5% | Strong (round-trip, shutdown, panic containment, process-group kill). |
| `monitorFailure` | 78.8% | Strong. |

Highest-value additions: a `deadlinePriorityQueue` + `Scheduler.schedule()` test (would have caught H1), and an `EntityService.handleGetBatch` dedup test.

---

## What is solid

- Parameterized SQL everywhere; `buildFilterClause` validates columns against an allowlist (no injection).
- JSONB metric path validated by regex before interpolation.
- Worker-pool design is well-hardened: ctx-aware `Submit`, per-job panic containment, process-group kill on timeout (`pluginWorker/pool.go`).
- Deadline-queue is O(log n) / O(n) batched.
- Request-reply RPCs bounded by context + `RPCTimeout` (`models/call.go`); HTTP handlers bounded by `rpcTimeout`.
- Discovery target expansion is correctly bounded (CIDR ≥ /16, range cap, `pending` reaping, IPv4-only).
- bcrypt admin auth with exponential-backoff login throttle; JWT HMAC with strict alg/issuer checks.
