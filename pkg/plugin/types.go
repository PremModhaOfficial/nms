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

	// Internal fields for discovery context (not sent to plugin)
	DiscoveryProfileID  int64 `json:"-"`
	CredentialProfileID int64 `json:"-"`
}

// Result is the output from a plugin binary.
type Result struct {
	DeviceID int64           `json:"device_id,omitempty"` // Echo back for correlation
	Target   string          `json:"target"`
	Port     int             `json:"port"`
	Success  bool            `json:"success"`
	Error    string          `json:"error,omitempty"`
	Hostname string          `json:"hostname,omitempty"` // Discovery mode
	Metrics  []Metric        `json:"metrics,omitempty"`  // Polling mode (legacy/flattened)
	Data     json.RawMessage `json:"data,omitempty"`     // Polling mode (hierarchical raw data)

	// Internal fields for provisioning context (set by discovery service)
	DiscoveryProfileID  int64 `json:"-"`
	CredentialProfileID int64 `json:"-"`
}

// Metric represents a single metric data point.
type Metric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}
