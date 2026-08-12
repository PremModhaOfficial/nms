# Dev dashboard prompt (caveman ultra)

Build Encore-style local dev dashboard for Go service. Developer view of internals. Served same-origin by Go binary. Three views: live topology graph of components, per-request distributed traces through components (request/response payloads each hop), API explorer calling every endpoint.

## Architecture context

Read ARCHITECTURE.md. Topology view exact visual replica of that component graph (every node, every edge). Edges ARE diagram. Each cross-component call instrumented span. Name per edge table below.

## Locked decisions (do not relitigate)

1. Telemetry: OpenTelemetry SDK, real spans at service boundaries. Span context (`trace.SpanContext`) propagate through service's own data structures (request/event/result structs). Async channel continuations link to parent HTTP trace.
2. Trace store: in-process ring buffer (cap 1000 traces) fed by custom OTel span exporter. No Jaeger/collector in dev. Exporter interface means collector added later without rework.
3. Auth: dashboard endpoints behind existing JWT middleware.
4. Serving: same-origin static files embedded in Go binary. No CORS. CSP strict `default-src 'none'` on API routes, loosened only for dashboard routes.
5. Frontend: zero-build vanilla HTML/CSS/JS. No framework, no bundler.
6. Masking: credentials/secret payloads never leave server. Masked `[HIDDEN]` in trace events, API responses.

## Instrumentation points (edges ARE diagram)

| Edge | Span name |
|------|-----------|
| HTTP request (middleware) | `http.<method> <path>` root span |
| API to EntityService RPC | `entityService.<op>` (child of HTTP) |
| API publish event (discovery run / provision) | `event.publish <type>` |
| EntityService to Scheduler (device events) | `scheduler.handleEvent` |
| Scheduler to Poller batch | `poller.processBatch` |
| Poller to PluginWorkerPool | `pluginPool.execute <plugin>` |
| PluginWorkerPool to MetricsService | `metrics.write` |
| MetricsService to DB | `metrics.query` |
| DiscoveryService to PluginWorkerPool | `discovery.run` |
| Results to HealthMonitor | `health.recordFailure` |
| HealthMonitor to EntityService (deactivate) | `entityService.<op>` |

Span context rides in request/event/result structs. Child span starts at consuming component with real parent. Async channel handoffs included.

## Deliverables

1. `pkg/tracex/`: OTel span model, custom span exporter, ring-buffer trace store, topology model. Unit tests for store.
2. Instrumentation across every service in edge table.
3. HTTP middleware capturing root span. `GET /api/v1/traces`, `GET /api/v1/topology` (JWT-protected).
4. `web/`: zero-build frontend. Topology SVG view (click trace, highlight components/edges traversed). Trace waterfall with payloads (masked). API explorer (call each endpoint, show request + response).
5. Static files embedded, served same-origin by Go binary.
6. `scripts/dev-dashboard.sh` one-shot run script (build, run, open browser). Makefile target.
7. Docs: `docs/dev-dashboard.md` with instrumentation table. README component diagram.

## Out of scope

- Production APM / Jaeger UI / OTLP exporter config
- Ops NMS UI (end-user device CRUD, dashboards, alerts)
- Auto-generated topology from static analysis. Topology declared explicitly in one file.

## Acceptance

- Dev script shows topology matching ARCHITECTURE.md exactly.
- API explorer requests produce traces whose spans follow edge table. Payloads visible each hop, secrets masked.
- Async continuations (scheduler, poller, pool) appear as child spans of originating HTTP request.
