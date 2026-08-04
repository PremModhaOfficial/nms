// Package plugin defines the contract between the core and external plugin binaries.
// Plugins receive Tasks via stdin (JSON array) and return Results via stdout (JSON array).
// The Credentials payload is protocol-specific and opaque to the core - plugins parse it themselves.
package plugin

import "encoding/json"

// Task is the input sent to a plugin binary.
type Task struct {
	DeviceID    int64           `json:"device_id,omitempty"`   // Optional: for tracking results back to a device
	Target      string          `json:"target"`                // IP address or hostname
	Port        int             `json:"port"`                  // Target port
	Credentials json.RawMessage `json:"credentials,omitempty"` // Decrypted JSON payload (protocol-specific)

	// Span context of the submitting service, so the pool can continue the
	// distributed trace while executing the batch.
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`
}

// Result is the output from a plugin binary.
type Result struct {
	DeviceID int64           `json:"device_id,omitempty"` // Echo back for correlation
	Target   string          `json:"target"`
	Port     int             `json:"port"`
	Success  bool            `json:"success"`
	Error    string          `json:"error,omitempty"`
	Hostname string          `json:"hostname,omitempty"` // Discovery mode
	Data     json.RawMessage `json:"data,omitempty"`     // Polling mode (hierarchical raw data)

	// Span context of the pool execution, so the consuming service can continue
	// the distributed trace from the pool result.
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`

	// Internal fields for provisioning context (set by discovery service)
	DiscoveryProfileID  int64 `json:"-"`
	CredentialProfileID int64 `json:"-"`
}

// SpanContextIDs returns the stamped OTel span context, letting the pool
// continue the producer's trace without coupling to models.
func (t Task) SpanContextIDs() (string, string) { return t.TraceID, t.SpanID }

// SetSpanContextIDs stamps the OTel span context onto a result so the
// consuming service can continue the trace across the result channel.
func (r *Result) SetSpanContextIDs(traceID, spanID string) {
	r.TraceID, r.SpanID = traceID, spanID
}
