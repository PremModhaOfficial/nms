## 4. A Window into the Machine

*A developer who can see the system live works ten times faster; build the window, but never let it leak secrets.*

## The Situation

NMS was a system you could only reason about from logs. The HTTP API, entity service, scheduler, poller, plugin worker pool, and health monitor all talked over channels, and ARCHITECTURE.md described the graph, but nothing showed it moving. When a discovery run failed, you grepped stdout and guessed which hop dropped the ball. Every bug was a two-hour archaeology session, and the failures you most wanted to understand left the least evidence. Real OTel spans already crossed every service boundary, but with no collector or UI they were invisible. What was missing was the window itself: a dashboard that turns the span stream into something you can click, plus the discipline to keep that window from showing secrets.

## The Transformation

Start with placement. The old router wrapped the mux in security middleware and stopped:

```go
// cmd/app/main.go — BEFORE
var h http.Handler = mux
h = api.SecurityHeaders()(h)
h = api.MaxBodyBytes(1 << 20)(h) // 1 MiB request body cap
return h
```

```go
// cmd/app/main.go — AFTER
root.Handle("/api/", apiHandler)
root.Handle("/login", apiHandler)
root.Handle("/", dashboardHandler())
return api.TraceMiddleware()(root)
```

The after wraps the outermost handler on purpose. Auth middleware sits inside the mux, so in the before world a failed login, a 404, or a 500 never passed through anything that recorded it; the failures were exactly the traces you could not see. Wrapping the root gives every request, including ones that die at the door, a root span. One detail shows the care: `r.Pattern` is only populated after ServeMux dispatch, so the middleware falls back to `r.URL.Path`. That is the difference between a trace that reads `HTTP POST /login` and one that reads `HTTP POST /` and tells you nothing.

Capturing bodies is where a window becomes a microscope, and where it becomes a liability. The before pipeline capped bodies with `http.MaxBytesReader` and dropped them, so you could reject a 10 MiB blob but never see why. The after reads and restores:

```go
// pkg/api/middleware.go — AFTER
if buf, err := io.ReadAll(io.LimitReader(r.Body, maxTraceRequestBody+1)); err == nil {
    r.Body = io.NopCloser(bytes.NewReader(buf))
    body := buf
    if len(body) > maxTraceRequestBody {
        body = body[:maxTraceRequestBody]
    }
    span.AddEvent("request.body", tracex.BodyEvent("request.body", tracex.MaskJSON(body)))
}
```

Read limit+1, not limit. Cap at exactly the limit and an oversized body comes back restored and truncated, sails past MaxBytesReader, and the API silently accepts bodies it used to reject. One extra byte preserves the rejection semantics. Then `MaskJSON` redacts recursively, keyed by `payload`, `password`, `secret`, `token`, `authorization`, matched case-insensitively with `strings.EqualFold`: both `Authorization` headers and `Password` fields become `[HIDDEN]`. The raw bytes never reach the span event, so the trace store and the dashboard only ever see the masked copy. Invalid JSON passes through unchanged; a panic in debug middleware is the worst trade. Response bodies get the same treatment through a `captureResponseWriter`: status recorded, copy capped at 64 KiB, binary content types skipped, `Unwrap()` exposed so `http.ResponseController` keeps working.

The dashboard is zero-build vanilla JS with three views, embedded via a module-root `//go:embed all:web` and served same-origin, no CORS, no second process. The API routes keep the strict `default-src 'none'` CSP; only the dashboard handler gets `script-src 'self'`. The topology SVG is a live replica of ARCHITECTURE.md, and clicking a trace pulses the components and edges that request crossed, with errors marked red. The traces list returns metadata only, spans dropped, limit clamped between 1 and 200, keeping polling cheap.

The two fixes that followed are the honest part. The waterfall view mutated the cached trace while building its tree, so a 5-span trace grew to 10, then 20, then 40 rows on every poll:

```js
// web/traces.js — BEFORE
var order = flattenTree(spans);

// web/traces.js — AFTER
var copies = spans.map(function (s) {
    return Object.assign({}, s, { events: s.events, attributes: s.attributes });
});
var order = flattenTree(copies);
```

The topology canvas drew nothing because the DOM builder used `document.createElement` for SVG tags, so the browser lowercased `viewBox` into `viewbox`; the fix routes SVG tags through `createElementNS`. Both are two-line bugs that cost hours of staring at a blank screen, and both prove the window works: it showed you its own bugs first. Finally, `scripts/dev-dashboard.sh` collapses the whole loop: check deps, ensure the DB, build, run from a pidfile, wait for the server, seed if empty, and with `--open` launch the browser. One command takes the stack from zero to visible.

## The Lesson

**Instrumentation is worthless until someone can look at it, so build the window early, and treat secrets as the one thing the window must never show.** The window pays for itself in the first debugging session, but only if it shows failures as well as successes, and only if redaction happens in the capture path rather than at display time; the capture layer is where bodies are read and the only place masking can be forgotten.
