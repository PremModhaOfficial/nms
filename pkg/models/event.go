package models

import "time"

// EventType defines the type of CRUD event.
type EventType string

const (
	EventCreate EventType = "create"
	EventUpdate EventType = "update"
	EventDelete EventType = "delete"

	// Command events for provisioning
	EventTriggerDiscovery EventType = "trigger_discovery"
	EventProvisionDevice  EventType = "provision_device"
	EventDeviceFailure    EventType = "device_failure" // Ping or poll failure
	EventRunDiscovery     EventType = "run_discovery"  // Explicitly run discovery for a profile
)

// Event represents a CRUD event for scheduler cache synchronization.
type Event struct {
	Type    EventType   `json:"type"`
	Payload interface{} `json:"payload"`
}

// DiscoveryTriggerEvent represents a command to trigger discovery
type DiscoveryTriggerEvent struct {
	DiscoveryProfileID int64
}

// DeviceProvisionEvent represents a command to provision a discovered device
type DeviceProvisionEvent struct {
	DeviceID               int64
	PollingIntervalSeconds int
}

// DeviceFailureEvent represents a device failure from ping or polling
type DeviceFailureEvent struct {
	DeviceID  int64
	Timestamp time.Time
	Reason    string // "ping" or "poll"
}
