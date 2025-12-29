package models

import (
	"encoding/json"
	"time"
)

// TableNamer is implemented by all models to provide their table name for sqlx queries.
type TableNamer interface {
	TableName() string
}

// Metric represents the metrics table storing raw poll results
type Metric struct {
	ID        int64           `db:"id" json:"id"`
	DeviceID  int64           `db:"device_id" json:"device_id"`
	Data      json.RawMessage `db:"data" json:"data"`
	Timestamp time.Time       `db:"timestamp" json:"timestamp"`
}

func (Metric) TableName() string { return "metrics" }

// CredentialProfile represents the credential_profiles table
type CredentialProfile struct {
	ID        int64     `db:"id" json:"id"`
	Name      string    `db:"name" json:"name" binding:"required"`
	Protocol  string    `db:"protocol" json:"protocol" binding:"required"`
	Payload   string    `db:"payload" json:"payload" binding:"required" gocrypt:"aes"` // Encrypted credential data
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// DiscoveryProfile represents the discovery_profiles table
type DiscoveryProfile struct {
	ID                  int64     `db:"id" json:"id"`
	Name                string    `db:"name" json:"name" binding:"required"`
	Target              string    `db:"target" json:"target" binding:"required"` // CIDR or IP
	Port                int       `db:"port" json:"port" binding:"required,min=1,max=65535"`
	CredentialProfileID int64     `db:"credential_profile_id" json:"credential_profile_id" binding:"required"`
	AutoProvision       bool      `db:"auto_provision" json:"auto_provision"`
	CreatedAt           time.Time `db:"created_at" json:"created_at"`
	UpdatedAt           time.Time `db:"updated_at" json:"updated_at"`

	// CredentialProfile is populated by cache lookup, not DB join
	CredentialProfile *CredentialProfile `db:"-" json:"credential_profile,omitempty"`
}

// Device represents the devices table
// Note: credential_profile_id and discovery_profile_id are immutable (validated in EntityService)
type Device struct {
	ID                     int64     `db:"id" json:"id"`
	Hostname               string    `db:"hostname" json:"hostname" update:"omitempty"`
	IPAddress              string    `db:"ip_address" json:"ip_address" binding:"omitempty,ip" update:"omitempty"`
	PluginID               string    `db:"plugin_id" json:"plugin_id" update:"omitempty"`
	Port                   int       `db:"port" json:"port" binding:"omitempty,min=1,max=65535" update:"omitempty"`
	CredentialProfileID    int64     `db:"credential_profile_id" json:"credential_profile_id" update:"omitempty"`
	DiscoveryProfileID     int64     `db:"discovery_profile_id" json:"discovery_profile_id" update:"omitempty"`
	PollingIntervalSeconds int       `db:"polling_interval_seconds" json:"polling_interval_seconds" binding:"omitempty,min=60,max=3600" update:"omitempty"`
	ShouldPing             bool      `db:"should_ping" json:"should_ping"`
	Status                 string    `db:"status" json:"status" binding:"omitempty,oneof=discovered active inactive error" update:"omitempty"`
	CreatedAt              time.Time `db:"created_at" json:"created_at"`
	UpdatedAt              time.Time `db:"updated_at" json:"updated_at"`

	// Populated by cache lookup, not DB join
	CredentialProfile *CredentialProfile `db:"-" json:"credential_profile,omitempty"`
	DiscoveryProfile  *DiscoveryProfile  `db:"-" json:"discovery_profile,omitempty"`
}

// TableName overrides the default table name logic
func (CredentialProfile) TableName() string { return "credential_profiles" }
func (DiscoveryProfile) TableName() string  { return "discovery_profiles" }
func (Device) TableName() string            { return "devices" }

// MetricQuery represents a request for metric data
type MetricQuery struct {
	Path  string    `json:"path"`  // JSON path (e.g., "cpu" or "cpu.total")
	Start time.Time `json:"start"` // start timestamp
	End   time.Time `json:"end"`   // end timestamp
	Limit int       `json:"limit"`
}
