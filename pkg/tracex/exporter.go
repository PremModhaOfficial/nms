package tracex

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	// maxSpansPerTrace caps spans retained per trace; extras are dropped.
	maxSpansPerTrace = 500
	// maxPendingTraces caps in-flight traces awaiting their root; oldest evicted.
	maxPendingTraces = 2000
	// maxBodyAttrBytes caps any single string attribute (bodies in particular).
	maxBodyAttrBytes = 64 << 10 // 64 KiB
)

// exporter is an sdktrace.SpanExporter that groups spans by trace ID in a
// pending map and finalizes a trace into the Store when its root span arrives
// (children always End before the parent in OTel, so root arrival means the
// trace is complete). It never panics and is safe for concurrent use.
type exporter struct {
	mu      sync.Mutex
	store   *Store
	pending map[string][]sdktrace.ReadOnlySpan
	order   []string // insertion order of pending trace IDs, for FIFO eviction
	closed  bool
}

// newExporter returns an exporter that finalizes traces into store.
func newExporter(store *Store) *exporter {
	return &exporter{
		store:   store,
		pending: make(map[string][]sdktrace.ReadOnlySpan),
		order:   make([]string, 0, 16),
	}
}

// ExportSpans ingests a batch of ended spans, finalizing any traces whose root
// arrived in this batch. Errors are unrecoverable by the SDK and intentionally
// never returned: a dev dashboard must not fail production spans.
func (e *exporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	defer e.recoverPanic("ExportSpans")
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	// Late child spans destined for already-finalized traces. Converted after
	// the pending bookkeeping so store locks are never held during store calls.
	var spansToAppend []sdktrace.ReadOnlySpan
	for _, sp := range spans {
		if sp == nil {
			continue
		}
		traceID := sp.SpanContext().TraceID().String()
		if traceID == "" {
			continue
		}
		if isRoot(sp) {
			children := e.pending[traceID]
			delete(e.pending, traceID)
			e.removeOrder(traceID)
			e.finalizeLocked(append(children, sp))
			continue
		}

		// Late child span: the root already ended and the trace was finalized
		// before this async continuation arrived. Append it to the stored trace
		// when present (no-op otherwise, matching old behavior for evicted ids).
		if e.finalizedTrace(traceID) {
			spansToAppend = append(spansToAppend, sp)
			continue
		}

		if _, ok := e.pending[traceID]; !ok {
			e.order = append(e.order, traceID)
		}
		e.pending[traceID] = append(e.pending[traceID], sp)
	}
	e.evictLocked()
	for _, sp := range spansToAppend {
		traceID := sp.SpanContext().TraceID().String()
		e.store.AppendSpan(traceID, convertSpan(sp, false))
	}
	return nil
}

// Shutdown flushes all pending (root-less) traces into the store and stops
// accepting new spans.
func (e *exporter) Shutdown(ctx context.Context) error {
	defer e.recoverPanic("Shutdown")
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	for id, spans := range e.pending {
		if len(spans) > 0 {
			// No root ever arrived; use the earliest span as the pseudo-root
			// so shutdown does not silently drop in-flight work.
			e.finalizeLocked(spans)
		}
		delete(e.pending, id)
	}
	e.order = e.order[:0]
	return nil
}

// isRoot reports whether sp is a root span: no valid parent, or explicitly
// marked with the nms.root=true attribute.
func isRoot(sp sdktrace.ReadOnlySpan) bool {
	if !sp.Parent().IsValid() {
		return true
	}
	for _, kv := range sp.Attributes() {
		if string(kv.Key) == "nms.root" && kv.Value.AsBool() {
			return true
		}
	}
	return false
}

// finalizedTrace reports whether a trace with traceID was already finalized
// into the store (root ended before this span arrived). Callers append late
// child spans rather than re-finalizing.
func (e *exporter) finalizedTrace(traceID string) bool {
	_, ok := e.store.Get(traceID)
	return ok
}

// finalizeLocked converts a completed span set into a Trace and stores it.
// The earliest-starting span is the root: roots begin before any child.
func (e *exporter) finalizeLocked(spans []sdktrace.ReadOnlySpan) {
	if len(spans) == 0 {
		return
	}
	sort.SliceStable(spans, func(i, j int) bool {
		si, sj := spans[i].StartTime(), spans[j].StartTime()
		if !si.Equal(sj) {
			return si.Before(sj)
		}
		return spans[i].SpanContext().SpanID().String() < spans[j].SpanContext().SpanID().String()
	})
	if len(spans) > maxSpansPerTrace {
		spans = spans[:maxSpansPerTrace] // drop extras, keeping the earliest
	}
	root := spans[0]
	t := &Trace{
		TraceID:    root.SpanContext().TraceID().String(),
		RootSpanID: root.SpanContext().SpanID().String(),
		StartedAt:  root.StartTime(),
		EndedAt:    root.EndTime(),
		DurationMS: durationMS(root.StartTime(), root.EndTime()),
		Spans:      make([]Span, 0, len(spans)),
	}
	extractRootFields(root, t)
	for i, sp := range spans {
		t.Spans = append(t.Spans, convertSpan(sp, i == 0))
	}
	t.ComponentIDs = componentIDs(t.Spans)
	t.SpanCount = len(t.Spans)
	e.store.Add(t)
}

// extractRootFields pulls request-level metadata off the root span's
// attributes.
func extractRootFields(root sdktrace.ReadOnlySpan, t *Trace) {
	for _, kv := range root.Attributes() {
		switch string(kv.Key) {
		case "http.method":
			if kv.Value.Type() == attribute.STRING {
				t.Method = kv.Value.AsString()
			}
		case "http.route":
			if kv.Value.Type() == attribute.STRING {
				t.Path = kv.Value.AsString()
			}
		case "http.status_code":
			if kv.Value.Type() == attribute.INT64 {
				t.StatusCode = int(kv.Value.AsInt64())
			}
		case "nms.trace.error":
			if kv.Value.Type() == attribute.BOOL {
				t.Error = kv.Value.AsBool()
			}
		}
	}
}

// convertSpan maps a ReadOnlySpan onto the dashboard Span shape. isRoot marks
// the root span, which defaults to the "api" component.
func convertSpan(sp sdktrace.ReadOnlySpan, isRoot bool) Span {
	component := "internal"
	if isRoot {
		component = "api"
	}
	attrs := make(map[string]any, len(sp.Attributes()))
	for _, kv := range sp.Attributes() {
		key := string(kv.Key)
		if key == "nms.component" {
			if v := kv.Value.AsString(); v != "" {
				component = v
			}
		}
		attrs[key] = flattenAttrValue(kv.Value)
	}
	events := make([]SpanEvent, 0, len(sp.Events()))
	for _, ev := range sp.Events() {
		evAttrs := make(map[string]any, len(ev.Attributes))
		for _, kv := range ev.Attributes {
			evAttrs[string(kv.Key)] = flattenAttrValue(kv.Value)
		}
		events = append(events, SpanEvent{Name: ev.Name, Time: ev.Time, Attributes: evAttrs})
	}
	parentID := ""
	if p := sp.Parent(); p.IsValid() {
		parentID = p.SpanID().String()
	}
	return Span{
		SpanID:     sp.SpanContext().SpanID().String(),
		ParentID:   parentID,
		Name:       sp.Name(),
		Kind:       kindString(sp.SpanKind()),
		Component:  component,
		StartedAt:  sp.StartTime(),
		EndedAt:    sp.EndTime(),
		DurationMS: durationMS(sp.StartTime(), sp.EndTime()),
		Attributes: attrs,
		Events:     events,
	}
}

// componentIDs returns the ordered unique set of traversed component node IDs,
// in span start order (spans are already sorted).
func componentIDs(spans []Span) []string {
	seen := make(map[string]bool, 8)
	ids := make([]string, 0, 8)
	for _, sp := range spans {
		c := sp.Component
		if c == "" || c == "internal" || seen[c] || !isNodeID(c) {
			continue
		}
		seen[c] = true
		ids = append(ids, c)
	}
	return ids
}

// flattenAttrValue converts an attribute.Value into a JSON-safe Go value:
// scalars keep their type, complex values collapse to a string.
func flattenAttrValue(v attribute.Value) any {
	switch v.Type() {
	case attribute.BOOL:
		return v.AsBool()
	case attribute.INT64:
		return v.AsInt64()
	case attribute.FLOAT64:
		return v.AsFloat64()
	case attribute.STRING:
		return truncateString(v.AsString())
	default:
		return truncateString(v.String())
	}
}

func truncateString(s string) string {
	if len(s) > maxBodyAttrBytes {
		return s[:maxBodyAttrBytes]
	}
	return s
}

func durationMS(start, end time.Time) float64 {
	return float64(end.Sub(start).Microseconds()) / 1000.0
}

func kindString(k trace.SpanKind) string {
	switch k {
	case trace.SpanKindInternal:
		return "internal"
	case trace.SpanKindServer:
		return "server"
	case trace.SpanKindClient:
		return "client"
	case trace.SpanKindProducer:
		return "producer"
	case trace.SpanKindConsumer:
		return "consumer"
	default:
		return "unspecified"
	}
}

// evictLocked drops the oldest pending traces until the pending map is within
// maxPendingTraces.
func (e *exporter) evictLocked() {
	for len(e.pending) > maxPendingTraces && len(e.order) > 0 {
		id := e.order[0]
		e.order = e.order[1:]
		if _, ok := e.pending[id]; ok {
			delete(e.pending, id)
		}
	}
}

// removeOrder drops one occurrence of id from the insertion-order queue.
func (e *exporter) removeOrder(id string) {
	for i, v := range e.order {
		if v == id {
			e.order = append(e.order[:i], e.order[i+1:]...)
			return
		}
	}
}

// recoverPanic guarantees the exporter never panics out of the SDK.
func (e *exporter) recoverPanic(op string) {
	if r := recover(); r != nil {
		slog.Error("tracex exporter recovered", "op", op, "error", r)
	}
}
