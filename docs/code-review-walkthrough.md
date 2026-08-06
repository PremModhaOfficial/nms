# Code-Level Walkthrough: Bad Practice → Good Practice

Before/after pairs extracted from the actual `29a8a60..HEAD` diff (refactor commit
`0e7d5ef`). "Before" is the parent commit, "After" is current HEAD.

---

## 1. Routing: gin framework magic → stdlib `net/http`

### Before (bad): framework-coupled, magic validation, blocking RPC

```go
// pkg/api/routes.go (before) - gin era
func RegisterEntityRoutes[T any](
	g *gin.RouterGroup, path string, entityType string, encryptionKey string,
	reqCh chan<- models.Request,
) {
	r := g.Group(path)
	r.GET("", listHandler[T](entityType, encryptionKey, reqCh))
	r.GET("/:id", getHandler[T](entityType, encryptionKey, reqCh))
	// ...
}

func listHandler[T any](entityType string, encryptionKey string, reqCh chan<- models.Request) gin.HandlerFunc {
	return func(c *gin.Context) {
		replyCh := make(chan models.Response, 1)
		reqCh <- models.Request{Operation: models.OpList, EntityType: entityType, ReplyCh: replyCh}
		resp := <-replyCh          // <-- blocks FOREVER if the service is stopped
		if resp.Error != nil {
			respondError(c, http.StatusInternalServerError, resp.Error.Error())
			return
		}
		c.JSON(http.StatusOK, decryptedItems)
	}
}
```

```go
// pkg/models/models.go (before) - validation embedded in struct tags
// Only works when gin runs the binding. Any other caller gets zero validation.
Name      string `db:"name" json:"name" binding:"required"`
Port      int    `db:"port" json:"port" binding:"required,min=1,max=65535"`
```

Problems: validation is dead code outside gin; a dead service hangs handlers
forever (goroutine leak); 200 OK on internal failure (`StatusInternalServerError`
from `respondError` on any service error); raw error strings leaked to clients.

### After (good): ServeMux patterns, bounded RPC, transport-independent validation

```go
// pkg/api/routes.go (after) - Go 1.22 ServeMux
func RegisterEntityRoutes[T any](
	register func(pattern string, h http.Handler),
	path string, entityType string, encryptionKey string,
	reqCh chan<- models.Request,
) {
	register("GET "+path, listHandler[T](entityType, encryptionKey, reqCh))
	register("GET "+path+"/{id}", getHandler[T](entityType, encryptionKey, reqCh))
	// ...
}

// doRequest bounds EVERY channel RPC with a context + deadline.
func doRequest(r *http.Request, reqCh chan<- models.Request, req models.Request) (models.Response, error) {
	ctx := r.Context()
	models.StampRequest(ctx, &req) // carries the HTTP span across the channel

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

```go
// pkg/Services/persistence/entityService.go (after) - validation in the service
if err := validateDeviceFields(device); err != nil { ... }
return models.Response{Error: fmt.Errorf("ip_address is required")}
```

Improvements: no framework; `rpcTimeout` (5s) + `ctx.Done()` on every RPC so a
stalled service returns 503 instead of hanging; validation lives where the data
is owned so it works over any transport; `classifyError` maps errors to real
status codes (404/409/503) and never forwards raw DB internals to clients.

---

## 2. Config: viper + yaml + 9 pool keys → stdlib env parsing + 3 shared keys

### Before (bad): framework + redundant file + duplicated pool config

```go
// pkg/config/config.go (before)
type Config struct {
	DBHost string `mapstructure:"DB_HOST"`
	// ... 30+ fields, every one with a mapstructure tag ...
	DBMaxOpenConns    int `mapstructure:"DB_MAX_OPEN_CONNS"`
	DBMaxIdleConns    int `mapstructure:"DB_MAX_IDLE_CONNS"`
	DBConnMaxLifeMins int `mapstructure:"DB_CONN_MAX_LIFE_MINS"`
	MetricsWriterMaxOpen int `mapstructure:"METRICS_WRITER_MAX_OPEN"` // 3 pools x 3 keys
	MetricsWriterMaxIdle int `mapstructure:"METRICS_WRITER_MAX_IDLE"`
	MetricsReaderMaxOpen int `mapstructure:"METRICS_READER_MAX_OPEN"`
	MetricsReaderMaxIdle int `mapstructure:"METRICS_READER_MAX_IDLE"`
}
// LoadConfig(path string) spins up a viper instance, reads app.yaml AND env...
```

Problems: viper pulls a whole transitive tree for what is 40 env reads; `app.yaml`
plus env overrides means two sources of truth that can disagree; 9 separate pool
tuning knobs nobody can meaningfully tune; config can't easily be tested.

### After (good): one `env()` helper, one pool config, no file

```go
// pkg/config/config.go (after)
func env(key, def string) string {
	if v := os.Getenv(key); v != "" { return v }
	return def
}

// Connection Pool Settings (shared by main, write, and read pools)
DBMaxOpenConns    int
DBMaxIdleConns    int
DBConnMaxLifeMins int
```

Improvements: 4 direct deps total (was 7 + giant transitive tree); single source
of truth (env); one set of pool limits applied to all three DB pools;
`LoadConfig()` is trivially unit-testable without a file.

---

## 3. Encryption: gocrypt + RawMessage workaround → stdlib AES-256-GCM

### Before (bad): third-party reflection magic with a hack on top

```go
// pkg/api/encryption.go (before)
opt := &gocrypt.Option{AESOpt: aesOpt}
gc := gocrypt.New(opt)
err = gc.Encrypt(&entity) // reflection magic, opaque wire format

// Special handling for json.RawMessage because gocrypt might have skipped it
// We'll use reflection to find json.RawMessage fields with gocrypt tag
if err := handleRawMessageFields(&entity, secretKey, true); err != nil { ... }
```

Problems: two workarounds stacked (string-only support + manual RawMessage
handling); wire format controlled by a third party; a tagged dependency
(`gocrypt:"aes"`) couples the data model to the crypto library.

### After (good): explicit crypto, readable format, drop-in compatible

```go
// pkg/api/encryption.go (after) - 64-hex key, nonce-prefixed, hex-encoded
func encryptString(aead cipher.AEAD, plain string) (string, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil { return "", err }
	ciphertext := aead.Seal(nonce, nonce, []byte(plain), nil)
	return hex.EncodeToString(ciphertext), nil
}
```

Improvements: stdlib only (`crypto/aes`, `crypto/cipher`); the wire format is
explicit (nonce + ciphertext, hex) and **matches gocrypt's exactly**, so stored
data decrypts with no migration; `EncryptStruct` still respects the old
`gocrypt:"aes"` tag, so the data model didn't need to change.

---

## 4. Server startup: duplicated branches → one server + production fail-fast

### Before (bad): copy-paste server blocks, warning-only security

```go
// cmd/app/main.go (before)
if conf.TLSCertFile != "" && conf.TLSKeyFile != "" {
	server = &http.Server{Addr: ":8443", Handler: router}
	go func() { _ = server.ListenAndServeTLS(...) }()
} else {
	server = &http.Server{Addr: ":8080", Handler: router}
	go func() { _ = server.ListenAndServe() }()
}
// ValidateSecrets() result is only a Warn() - production boots with default
// admin passwords and no TLS.
```

### After (good): one path, refuses to boot unsafely

```go
// cmd/app/main.go (after)
if err := conf.ValidateSecrets(); err != nil {
	if os.Getenv("APP_ENV") == "production" {
		slog.Error("Refusing to start with insecure secrets", "error", err)
		os.Exit(1)          // <-- fail fast
	}
	slog.Warn("Security validation warning", "error", err)
}
if os.Getenv("APP_ENV") == "production" && (conf.TLSCertFile == "" || conf.TLSKeyFile == "") {
	slog.Error("Refusing to start in production without TLS ...")
	os.Exit(1)
}
server := newHTTPServer(addr, router) // one construction, one goroutine
```

Improvements: security validation runs BEFORE services start; production refuses
to expose JWTs/encryption keys over plain HTTP or with default credentials; one
server object instead of two copy-pasted blocks.

---

## 5. Observability additions (new good practice, tracex)

These have no "bad before" - they're net-new patterns worth learning from.

### Span context rides in the message structs, not globals

```go
// pkg/models/spancontext.go
func StampRequest(ctx context.Context, req *Request) { req.TraceID, req.SpanID = SpanContextIDs(ctx) }

// Receiver side: rebuild a remote context from the ids carried on the message.
func RemoteContext(traceID, spanID string) context.Context {
	return WithRemoteSpanContext(context.Background(), traceID, spanID)
}
```

Why good: W3C trace/span IDs in plain structs cross goroutine/channel boundaries
without contorting `context.Context` through channels; invalid/empty ids degrade
to `context.Background()` instead of crashing.

### Ring buffer store: bounded, copy-on-read, never aliases

```go
// pkg/tracex/store.go
func (s *Store) List(limit int) []Trace { ... cloneTrace(s.buf[idx]) ... } // deep copy
func (s *Store) AppendSpan(traceID string, sp Span) bool { ... dedupe by SpanID ... }
```

Why good: `TraceBufferSize = 1000` bounds memory; every read returns a deep copy
so callers can't mutate stored state; `AppendSpan` merges late async children
into finalized traces and skips duplicates.

### Trace middleware: capture body, restore it, mask secrets, bound size

```go
// pkg/api/middleware.go
if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
	if buf, err := io.ReadAll(io.LimitReader(r.Body, maxTraceRequestBody+1)); err == nil {
		r.Body = io.NopCloser(bytes.NewReader(buf)) // restore for the handler
		span.AddEvent("request.body", tracex.BodyEvent("request.body", tracex.MaskJSON(body)))
	}
}
```

Why good: reads `limit+1` bytes so `MaxBytesReader` still trips on oversized
bodies (preserves rejection semantics); body is restored before the handler;
`MaskJSON` redacts `payload/password/secret/token/authorization` keys
case-insensitively so credentials never leave the server; 64 KiB cap on the
stored attribute.
