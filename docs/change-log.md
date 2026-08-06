# Change Log: 29a8a60 → HEAD

Categorized walkthrough of all changes since `29a8a60` (2026-01-08, "Add DeepWiki badge to README").
14 commits, 68 files, +7854 / −1778 lines. Covers 2026-07-31 → 2026-08-04.

## Chunk 1 — Dependency diet & stdlib migration (`0e7d5ef`)

The foundation. One large refactor commit, 42 files, +2585 / −1769.

- **Routing: gin → stdlib `net/http`.** `pkg/api/routes.go` uses Go 1.22 ServeMux method patterns and `PathValue`; binding validation moved into `EntityService`. Direct deps: 7 → 4; the gin/viper/gocrypt transitive tree (sonic, quic-go, protobuf, etc.) is gone.
- **Config: viper → env vars.** `config.LoadConfig()` is plain env parsing; 7 DB-pool keys collapsed into 3; `app.yaml` deleted (env overrides it).
- **Encryption: gocrypt → stdlib AES-256-GCM.** Identical wire format, no data migration. Dead branches removed (unreachable `RawMessage` path, decrypt-on-read path).
- **Server hardening** (`cmd/app/main.go`): duplicate TLS/plain-HTTP branches merged into one server; fail-fast in production on weak secrets or missing TLS (the API serves JWTs and encryption keys).
- **Service layer**: new `pkg/models/call.go` Op\* operation model; discovery, health monitor, entity service, metrics service, poller, scheduler hardened. `sanitizeJSON` BOM-order fix.
- **Plugin layer**: `pool.go` reworked, winrm plugin protocol cleaned.
- **Dead weight deleted**: `flow.md`, `mirror.md`, `progress.md`, `app.yaml`, `seed.sh`, `added_endpoints.txt`, tracked `app.log`/`.env`.
- **Tests from ~zero to real**: 8 new test files — `api_test.go` (308 lines, rewritten on `httptest`), `pluginWorker/pool_test.go` (203), discovery/health/entity/repository/encryption, winrm `main_test.go`.
- **go.mod**: gin, viper, gocrypt, sonic, quic-go dropped; jwt, sqlx, pgx kept; pgx 5.7.6 → 5.9.2; Go 1.25.5 → 1.26.4.

## Chunk 2 — Agent-skills scaffold (`20ce977`, `9e90ccf`)

`AGENTS.md` + `docs/agents/{domain,issue-tracker,triage-labels}.md`.

- Codifies repo conventions for AI agents: issues/PRDs live in `PremModhaOfficial/nms` via `gh`, triage label vocabulary (`needs-triage`, `ready-for-agent`, ...), single-context domain docs (`CONTEXT.md` + `docs/adr/`).
- README gains the mermaid component diagram; links to `ARCHITECTURE.md` and the dev dashboard.
- `docs/dev-dashboard.md` documents the instrumentation edge table and how to run the dashboard.

## Chunk 3 — Observability core: `pkg/tracex` + instrumentation (`49ca57b`, `f4a9efb`, `7deb9c4`, `fe97fb1`)

- **In-process OTel store**: bounded ring buffer (cap 1000), thread-safe, always deep-copies. No Jaeger/collector in dev.
- **Custom `SpanExporter`**: groups spans by trace ID (pending cap 2000, FIFO eviction), finalizes on root arrival. Implements the OTel `SpanExporter` interface, so a real collector swaps in later with zero rework.
- **Span context across channels** (`pkg/models/spancontext.go`): W3C trace/span IDs ride in `Request`/`Event`/`Result` structs (`StampRequest`/`StampEvent`); consumers rebuild remote context (`WithRemoteSpanContext`) so child spans get real parents across goroutine boundaries.
- **Async continuation linking** (`7deb9c4`): late child spans from scheduler/poller/pool continuations merge into already-finalized traces via `Store.AppendSpan` (dedupe by span ID). Guards: 500 spans/trace, 64 KiB body cap.
- **Topology model**: static graph, 10 nodes / 14 edges, exact replica of ARCHITECTURE.md (fix `fe97fb1` added the missing `discoverypool` node). Node IDs are the exporter's contract.
- **Tests**: `store_test.go` (214 lines) — ring buffer, append, deep-clone, dedupe.

## Chunk 4 — Dev dashboard surface (`f653b75`, `bec77c5`, `3bbcc4d`, `cb97135`, `f2acad0`, `ab35bd2`, `a6766c6`)

- **HTTP trace capture**: `TraceMiddleware` wraps the outermost handler (auth failures and 4xx/5xx traced too). Root span `HTTP <method> <path>`; request/response bodies captured (1 MiB cap), secrets masked `[HIDDEN]` via `MaskJSON` (payload, password, secret, token, authorization, case-insensitive).
- **Endpoints**: `GET /api/v1/topology`, `GET /api/v1/traces?limit=` (default 50, cap 200, metadata-only), `GET /api/v1/traces/{id}` (full span tree). JWT-protected.
- **Static serving**: files embedded via `embed.FS`, same-origin; CSP strict `default-src 'none'` on API routes, loosened for the dashboard. Zero-build vanilla JS.
- **Frontend** (2,924 lines): topology SVG with click-a-trace-to-highlight, trace waterfall with masked payloads, API explorer calling every endpoint.
- **Script**: `scripts/dev-dashboard.sh` one-shot runner — check deps → ensure DB + idempotent schema → build → run (pidfile, `app.log`) → wait ≤30s → seed-if-empty → print URL. Flags `--smoke`, `--no-seed`, `--open`, `--stop`. Makefile targets `dashboard` / `dashboard-smoke`.
- **Fixes**: SVG namespace rendering (`f2acad0`), component chips + waterfall spans (`cb97135`).

## Verified state (2026-08-06)

- `go build ./...` passes.
- `go test ./pkg/tracex/... ./pkg/api/... ./pkg/pluginWorker/...` passes.

## Loose ends

- `dev-dashboard-prompt.md` (the dashboard spec) is untracked. Commit it to make the dashboard work auditable.
- `docs/dev-dashboard.md` mentions the instrumentation table; keep it in sync with `pkg/tracex/topology.go` if edges change.
