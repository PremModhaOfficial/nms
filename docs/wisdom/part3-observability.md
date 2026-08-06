# 3. Making the Invisible Visible

*An event-driven service is a black box until every hop carries its trace; the edges are the diagram.*

## The Situation

NMS is a fan-out of goroutines joined by channels. A discovery request lands in the API layer, gets handed to EntityService, hops to the Scheduler, crosses into the Poller, drops into the plugin worker pool, and eventually writes through MetricsService into PostgreSQL. Every hop is a `select` on a channel, and every hop was a place where the story could die silently.

When a device went quiet or a discovery run produced nothing, you had logs per component but no single answer to "where did this request go?" The failure could hide anywhere along that chain, and finding it meant reading five services' worth of `slog` output by hand, matching timestamps, and guessing. Worse, the payloads that would have told you what each hop carried included credentials, and nobody wanted those in a log file. The cost of observability was measured in hours per incident, and the alternative, logging everything, was unacceptable.

## The Transformation

The fix was not a logging framework. It was four small decisions that compound: a real OpenTelemetry exporter writing into an in-process ring buffer, span context stamped onto every channel message, remote contexts rebuilt at each consumer, and a topology graph that is literally the architecture diagram.

The first decision was to make the channel messages themselves carry the trace. An `Event` used to be just a type and a payload. Nothing in it said where it came from.

BEFORE (pkg/models/event.go, parent of f4a9efb):

```go
type Event struct {
	Type    EventType   `json:"type"`
	Payload interface{} `json:"payload"`
}
```

AFTER (pkg/models/event.go, f4a9efb):

```go
type Event struct {
	Type    EventType   `json:"type"`
	Payload interface{} `json:"payload"`

	// Span context of the sender, so the receiving service can continue the
	// distributed trace across the channel boundary.
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`
}
```

Before, the receiver started each handler with whatever context happened to be lying around, so every component began a brand new trace and the only shared identifier was a device ID you happened to notice in two log lines. After, the producer stamps the message with `StampEvent` and the consumer rebuilds a real parent at the point of consumption, for example `tracex.Start(models.RemoteContext(event.TraceID, event.SpanID), "entity", "entityService.handleEvent")`. W3C trace and span ids ride the message itself, so child spans get genuine parents across goroutine boundaries.

The second decision handled awkward timing. Async continuations like the scheduler, poller, and plugin pool end after the HTTP root span, which means the store had already finalized the trace by the time the children arrived. The old exporter path could not cope: every non-root span went into the pending map.

BEFORE (pkg/tracex/exporter.go, parent of 7deb9c4):

```go
if _, ok := e.pending[traceID]; !ok {
	e.order = append(e.order, traceID)
}
e.pending[traceID] = append(e.pending[traceID], sp)
```

AFTER (pkg/tracex/store.go, 7deb9c4):

```go
// Skip duplicates (retried exports or double-finalized spans).
for _, existing := range t.Spans {
	if existing.SpanID == cp.SpanID {
		return true
	}
}
t.Spans = append(t.Spans, cp)
t.SpanCount = len(t.Spans)
if t.EndedAt.Before(cp.EndedAt) {
	t.EndedAt = cp.EndedAt
}
```

Before, a late child span waited in `pending` for a root that would never come, and was evicted unseen, so a waterfall that ended at the API layer showed a request that supposedly did nothing. After, the exporter checks `finalizedTrace` and routes the late span into `AppendSpan`, which merges it into the stored trace with span-ID dedupe so a retried export never doubles a span, and recomputes end time and component list from the merged set.

The third decision made the picture itself checkable. The topology view was declared by hand, and the first version drifted from ARCHITECTURE.md: nine nodes, a discovery pool missing, and edge labels that no longer matched the channel wiring.

BEFORE (pkg/tracex/topology.go, parent of fe97fb1):

```go
{ID: "discovery", Label: "DiscoveryService", Type: "service"},
{ID: "health", Label: "HealthMonitor", Type: "service"},
...
{From: "discovery", To: "pluginpool", Label: "jobs"},
```

AFTER (pkg/tracex/topology.go, fe97fb1):

```go
{ID: "discoverypool", Label: "DiscoveryWorkerPool", Type: "service"},
...
{From: "discovery", To: "discoverypool", Label: "Jobs"},
{From: "discoverypool", To: "entity", Label: "Results"},
```

Before, the dashboard and the architecture document disagreed, and a viewer could not tell which one was stale. After, the graph is an exact ten-node, fourteen-edge replica of the ARCHITECTURE.md mermaid diagram, and `isNodeID` enforces that only those node ids may appear in a trace's `ComponentIDs`. The diagram and the telemetry share one contract, so a trace that visits a node not on the map is a bug you can see, not a mystery.

Underneath all of this sit the guards that keep a dev tool from becoming a production liability: a ring buffer capped at 1000 traces that deep-copies on the way in and out so it never aliases caller state, 500 spans per trace, a 64 KiB cap on body attributes, 2000 pending traces awaiting their root, and `MaskJSON` redacting `payload`, `password`, `secret`, `token`, and `authorization` before anything is stored. The exporter implements the standard OTel `SpanExporter` interface, so wiring in a real collector later is a configuration swap, not a rewrite. `store_test.go` locks down ring eviction, newest-first listing, deep-copy semantics, and root-arrival finalization.

## The Lesson

**Instrument the hop, not just the node, because in an event-driven service the edges are where the work happens and where it disappears.**

The trace only becomes useful when the message itself is the carrier, the late children still get merged, and the graph you draw on the dashboard is the same contract the code enforces. That is the difference between an architecture diagram and a picture of how the system actually behaves.
