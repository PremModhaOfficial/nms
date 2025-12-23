package models

import (
	"encoding/json"
	"time"
)

// Metric represents the metrics table storing raw poll results
type Metric struct {
	ID        int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	MonitorID int64           `gorm:"not null" json:"monitor_id"`
	Data      json.RawMessage `gorm:"type:jsonb;not null" json:"data"`
	Timestamp time.Time       `gorm:"default:CURRENT_TIMESTAMP" json:"timestamp"`
}

func (Metric) TableName() string { return "metrics" }

// CredentialProfile represents the credential_profiles table
type CredentialProfile struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"not null" json:"name" binding:"required"`
	Description string    `json:"description"`
	Protocol    string    `gorm:"not null" json:"protocol" binding:"required"`
	Payload     string    `gorm:"not null" json:"payload" binding:"required" gocrypt:"aes"` // Encrypted credential data
	CreatedAt   time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt   time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"updated_at"`
}

// DiscoveryProfile represents the discovery_profiles table
type DiscoveryProfile struct {
	ID                  int64              `gorm:"primaryKey;autoIncrement" json:"id"`
	Name                string             `gorm:"not null" json:"name" binding:"required"`
	Target              string             `gorm:"not null" json:"target" binding:"required"` // CIDR or IP
	Port                int                `gorm:"not null" json:"port" binding:"required,min=1,max=65535"`
	CredentialProfileID int64              `gorm:"not null" json:"credential_profile_id" binding:"required"`
	CredentialProfile   *CredentialProfile `gorm:"foreignKey:CredentialProfileID" json:"credential_profile,omitempty"`
	AutoProvision       bool               `gorm:"default:false" json:"auto_provision"`
	AutoRun             bool               `gorm:"default:false" json:"auto_run"`
	CreatedAt           time.Time          `gorm:"default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt           time.Time          `gorm:"default:CURRENT_TIMESTAMP" json:"updated_at"`
}

// Device represents the devices table
type Device struct {
	ID                 int64             `gorm:"primaryKey;autoIncrement" json:"id"`
	DiscoveryProfileID int64             `gorm:"not null" json:"discovery_profile_id" binding:"required"`
	DiscoveryProfile   *DiscoveryProfile `gorm:"foreignKey:DiscoveryProfileID" json:"discovery_profile,omitempty"`
	Hostname           string            `json:"hostname"`
	IPAddress          string            `gorm:"not null;type:inet" json:"ip_address" binding:"required,ip"`
	Port               int               `gorm:"not null" json:"port" binding:"required"`
	Status             string            `gorm:"default:'new'" json:"status"`
	CreatedAt          time.Time         `gorm:"default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt          time.Time         `gorm:"default:CURRENT_TIMESTAMP" json:"updated_at"`
}

// Monitor represents the monitors table
type Monitor struct {
	ID                     int64              `gorm:"primaryKey;autoIncrement" json:"id"`
	Hostname               string             `json:"hostname"`
	IPAddress              string             `gorm:"not null;type:inet" json:"ip_address" binding:"required,ip"`
	PluginID               string             `gorm:"not null" json:"plugin_id" binding:"required"`
	Port                   int                `gorm:"not null;default:0" json:"port"`
	CredentialProfileID    int64              `gorm:"not null" json:"credential_profile_id" binding:"required"`
	CredentialProfile      *CredentialProfile `gorm:"foreignKey:CredentialProfileID" json:"credential_profile,omitempty"`
	DiscoveryProfileID     int64              `gorm:"not null" json:"discovery_profile_id" binding:"required"`
	DiscoveryProfile       *DiscoveryProfile  `gorm:"foreignKey:DiscoveryProfileID" json:"discovery_profile,omitempty"`
	PollingIntervalSeconds int                `gorm:"default:60" json:"polling_interval_seconds" binding:"min=1"`
	Status                 string             `gorm:"default:'active'" json:"status" binding:"oneof=active inactive error"`
	CreatedAt              time.Time          `gorm:"default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt              time.Time          `gorm:"default:CURRENT_TIMESTAMP" json:"updated_at"`
}

// TableName overrides the default table name logic
func (CredentialProfile) TableName() string { return "credential_profiles" }
func (DiscoveryProfile) TableName() string  { return "discovery_profiles" }
func (Device) TableName() string            { return "devices" }
func (Monitor) TableName() string           { return "monitors" }

// MetricQuery represents a request for metric data
type MetricQuery struct {
	Path  string    `json:"path"`  // JSON path (e.g., "cpu" or "cpu.total")
	Start time.Time `json:"start"` // start timestamp
	End   time.Time `json:"end"`   // end timestamp
	Limit int       `json:"limit"`
}

// GetID methods to satisfy Identifiable interface
func (c CredentialProfile) GetID() int64 { return c.ID }
func (d DiscoveryProfile) GetID() int64  { return d.ID }
func (d Device) GetID() int64            { return d.ID }
func (m Monitor) GetID() int64           { return m.ID }
