package tracex

import "time"

// Trace is a finalized in-process trace as consumed by the dev dashboard.
// The JSON shape is the frontend contract; do not rename fields without a
// coordinated frontend change.
type Trace struct {
	TraceID      string    `json:"trace_id"`
	RootSpanID   string    `json:"root_span_id"`
	StartedAt    time.Time `json:"started_at"`
	EndedAt      time.Time `json:"ended_at"`
	DurationMS   float64   `json:"duration_ms"`
	Method       string    `json:"method,omitempty"`
	Path         string    `json:"path,omitempty"`
	StatusCode   int       `json:"status_code,omitempty"`
	ComponentIDs []string  `json:"component_ids"`
	SpanCount    int       `json:"span_count"`
	Error        bool      `json:"error"`
	Spans        []Span    `json:"spans"`
}

// Span is a single OpenTelemetry span converted into the dashboard shape.
type Span struct {
	SpanID     string         `json:"span_id"`
	ParentID   string         `json:"parent_id,omitempty"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Component  string         `json:"component"` // node id: api, entity, scheduler, poller, pluginpool, metrics, db, discovery, health
	StartedAt  time.Time      `json:"started_at"`
	EndedAt    time.Time      `json:"ended_at"`
	DurationMS float64        `json:"duration_ms"`
	Attributes map[string]any `json:"attributes,omitempty"`
	Events     []SpanEvent    `json:"events,omitempty"`
}

// SpanEvent is a timed event recorded on a span (e.g. a request body).
type SpanEvent struct {
	Name       string         `json:"name"`
	Time       time.Time      `json:"time"`
	Attributes map[string]any `json:"attributes,omitempty"`
}
