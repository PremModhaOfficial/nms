package models

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

// RemoteContext builds a context carrying the remote span context that rode
// across a channel boundary in a message's TraceID/SpanID fields. The base is
// intentionally a fresh context - channel messages carry no cancellation or
// deadline, only tracing state. Empty or invalid ids yield Background.
func RemoteContext(traceID, spanID string) context.Context {
	return WithRemoteSpanContext(context.Background(), traceID, spanID)
}

// WithRemoteSpanContext layers a remote span context (from TraceID/SpanID) on
// top of base, preserving base's cancellation and deadline. Empty or invalid
// ids leave base unchanged.
func WithRemoteSpanContext(base context.Context, traceID, spanID string) context.Context {
	sc := RemoteSpanContext(traceID, spanID)
	if !sc.IsValid() {
		return base
	}
	return trace.ContextWithRemoteSpanContext(base, sc)
}

// RemoteSpanContext parses trace_id/span_id hex strings into a remote, sampled
// span context. Returns an invalid SpanContext for empty or malformed input.
func RemoteSpanContext(traceID, spanID string) trace.SpanContext {
	// W3C trace-context ids: 32 hex chars for the trace id, 16 for the span id.
	if len(traceID) != 32 || len(spanID) != 16 {
		return trace.SpanContext{}
	}
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		return trace.SpanContext{}
	}
	sid, err := trace.SpanIDFromHex(spanID)
	if err != nil {
		return trace.SpanContext{}
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
}

// SpanContextIDs returns the trace_id and span_id of the current span in ctx,
// or empty strings when ctx carries no valid span. Use when stamping outgoing
// messages so the receiver can continue the trace.
func SpanContextIDs(ctx context.Context) (string, string) {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return "", ""
	}
	return sc.TraceID().String(), sc.SpanID().String()
}

// StampRequest carries the current span's context on a Request so the
// receiving service can continue the trace across the reply-channel boundary.
func StampRequest(ctx context.Context, req *Request) {
	req.TraceID, req.SpanID = SpanContextIDs(ctx)
}

// StampEvent carries the current span's context on an Event so the consuming
// service can continue the trace across the event-channel boundary.
func StampEvent(ctx context.Context, ev *Event) {
	ev.TraceID, ev.SpanID = SpanContextIDs(ctx)
}
