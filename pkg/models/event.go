package models

// EventType defines the type of CRUD event.
type EventType string

const (
	EventCreate   EventType = "create"
	EventUpdate   EventType = "update"
	EventDelete   EventType = "delete"
	EventAnything EventType = "*"

	// Command events for manual provisioning
	EventTriggerDiscovery EventType = "trigger_discovery"
	EventProvisionDevice  EventType = "provision_device"
)

// Event represents a CRUD event for scheduler cache synchronization.
type Event struct {
	Type    EventType   `json:"type"`
	Payload interface{} `json:"payload"`
}

// DiscoveryTriggerCommand represents a command to trigger discovery
type DiscoveryTriggerCommand struct {
	DiscoveryProfileID int64
}

// DeviceProvisionCommand represents a command to provision a device
type DeviceProvisionCommand struct {
	DeviceID               int64
	CredentialProfileID    int64
	PollingIntervalSeconds int
}
