package models

// Operation types for request-reply communication
const (
	OpList   = "list"
	OpGet    = "get"
	OpCreate = "create"
	OpUpdate = "update"
	OpDelete = "delete"
	OpQuery  = "query" // For metrics
)

// Request is a point-to-point message with reply channel for synchronous communication
type Request struct {
	Operation  string        // list, get, create, update, delete, query
	EntityType string        // "Device", "CredentialProfile", "DiscoveryProfile", "Metric"
	ID         int64         // For get/update/delete
	Payload    interface{}   // Entity or query params
	ReplyCh    chan Response // Caller waits on this for synchronous reply
}

// Response contains result or error from service layer
type Response struct {
	Data  interface{}
	Error error
}
