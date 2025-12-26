package models

// Operation types for request-reply communication
const (
	OpList   = "list"
	OpGet    = "get"
	OpCreate = "create"
	OpUpdate = "update"
	OpDelete = "delete"
	OpQuery  = "query" // For metrics

	// Scheduler/Poller operations
	OpGetBatch      = "get_batch"      // Batch lookup by IDs, returns devices split by should_ping
	OpGetCredential = "get_credential" // Get credential by profile ID
)

// Request is a point-to-point message with reply channel for synchronous communication
type Request struct {
	Operation  string        // list, get, create, update, delete, query, get_batch, get_credential
	EntityType string        // "Device", "CredentialProfile", "DiscoveryProfile", "Metric"
	ID         int64         // For get/update/delete
	IDs        []int64       // For batch operations (get_batch)
	Payload    interface{}   // Entity or query params
	ReplyCh    chan Response // Caller waits on this for synchronous reply
}

// Response contains result or error from service layer
type Response struct {
	Data  interface{}
	Error error
}

// BatchDeviceResponse is the payload for OpGetBatch responses
// Returns devices split by should_ping flag
type BatchDeviceResponse struct {
	ToPing []*Device // Devices that require fping check (should_ping=true)
	ToSkip []*Device // Devices that skip fping (should_ping=false)
}
