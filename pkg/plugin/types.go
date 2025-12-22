// Package plugin defines the contract between the core and external plugin binaries.
// Plugins receive Tasks via stdin (JSON array) and return Results via stdout (JSON array).
// The Credentials payload is protocol-specific and opaque to the core - plugins parse it themselves.
package plugin

// Task is the input sent to a plugin binary.
type Task struct {
	MonitorID   int64  `json:"monitor_id,omitempty"`  // Optional: for tracking results back to a monitor
	Target      string `json:"target"`                // IP address or hostname
	Port        int    `json:"port"`                  // Target port
	Credentials string `json:"credentials,omitempty"` // Decrypted JSON payload (protocol-specific)
}

// Result is the output from a plugin binary.
type Result struct {
	MonitorID int64    `json:"monitor_id,omitempty"` // Echo back for correlation
	Target    string   `json:"target"`
	Port      int      `json:"port"`
	Success   bool     `json:"success"`
	Error     string   `json:"error,omitempty"`
	Hostname  string   `json:"hostname,omitempty"` // Discovery mode
	Metrics   []Metric `json:"metrics,omitempty"`  // Polling mode
}

// Metric represents a single metric data point.
type Metric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}
