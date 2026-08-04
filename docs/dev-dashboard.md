# Dev Dashboard for NMS (frontend)

Decisions locked in grilling session (2026-08-04). Single codebase, no issue-map machinery.

## Destination

Encore-style local dev dashboard for the NMS backend, served same-origin by the Go server:

- **Topology view**: exact visual replica of `ARCHITECTURE.md` component graph (API, EntityService, Scheduler, Poller, PluginWorkerPool, MetricsService, DB, DiscoveryService, HealthMonitor). Live: selected trace highlights the components and edges it traversed.
- **Trace view**: per-request distributed traces showing the flow through components, with request/response payloads at each hop (credentials masked `[HIDDEN]`).
- **API explorer**: call every endpoint in the service (login, credentials/devices/discovery_profiles CRUD, run discovery, provision, metrics query), see request + response.

## Locked decisions

1. **Frontend purpose**: developer-facing dev dashboard (Encore `localhost:9400` clone). Not an ops NMS UI.
2. **Telemetry**: OpenTelemetry SDK, real spans at service boundaries. Hard-to-get-right parts (context, parent-child, sampling) come from OTel.
3. **Trace store**: in-process ring buffer (cap 1000 traces) fed by a custom OTel span exporter. Go serves `GET /api/v1/traces` and `GET /api/v1/topology`. No Jaeger/collector in dev; OTel exporter interface means a collector can be added later without rework.
4. **Auth**: dashboard uses the existing JWT login; traces/topology endpoints sit behind the same middleware.
5. **Serving**: same-origin static files from the Go binary (no CORS needed). CSP loosened only for the dashboard routes; API routes keep strict `default-src 'none'`.
6. **Frontend stack**: zero-build vanilla HTML/CSS/JS. No framework, no bundler.
7. **Masking**: credential payloads never leave the server. API already masks responses; trace events mask `payload` fields.

## Instrumentation points (the edges ARE the diagram)

| Edge | Span name |
|------|-----------|
| HTTP request (middleware) | `http.<method> <path>` root span |
| API -> EntityService RPC | `entityService.<op>` (child of HTTP) |
| API publish event (discovery run / provision) | `event.publish <type>` |
| EntityService -> Scheduler (device events) | `scheduler.handleEvent` |
| Scheduler -> Poller batch | `poller.processBatch` |
| Poller -> PluginWorkerPool | `pluginPool.execute <plugin>` |
| PluginWorkerPool -> MetricsService | `metrics.write` |
| MetricsService -> DB | `metrics.query` |
| DiscoveryService -> PluginWorkerPool | `discovery.run` |
| Poller/Results -> HealthMonitor | `health.recordFailure` |
| HealthMonitor -> EntityService (deactivate) | `entityService.<op>` |

Span context (`trace.SpanContext`) rides inside `models.Request`, `models.Event`, `plugin.Result` so a child span can be started at the consuming service with the real parent.

## Out of scope

- Production APM / Jaeger UI / OTLP exporter config.
- Ops NMS UI (device CRUD forms for end users, dashboards, alerts).
- Auto-generated topology from static analysis (Encore has a compiler hook; NMS declares topology explicitly in one file).
- Logs/metrics dashboards beyond traces.
