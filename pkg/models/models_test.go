package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMetric(t *testing.T) {
	now := time.Now()
	rawData := json.RawMessage(`{"cpu": 50, "memory": 75}`)

	tests := []struct {
		name     string
		metric   Metric
		wantErr  bool
		validate func(Metric, *testing.T)
	}{
		{
			name: "valid metric",
			metric: Metric{
				ID:        1,
				MonitorID: 10,
				Data:      rawData,
				Timestamp: now,
			},
			wantErr: false,
			validate: func(m Metric, t *testing.T) {
				assert.Equal(t, int64(1), m.ID)
				assert.Equal(t, int64(10), m.MonitorID)
				assert.Equal(t, rawData, m.Data)
				assert.WithinDuration(t, now, m.Timestamp, time.Second)
			},
		},
		{
			name: "zero values metric",
			metric: Metric{
				ID:        0,
				MonitorID: 0,
				Data:      json.RawMessage(`{}`),
				Timestamp: time.Time{},
			},
			wantErr: false,
			validate: func(m Metric, t *testing.T) {
				assert.Equal(t, int64(0), m.ID)
				assert.Equal(t, int64(0), m.MonitorID)
				assert.Equal(t, json.RawMessage(`{}`), m.Data)
				assert.True(t, m.Timestamp.IsZero())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(tt.metric, t)
			
			// Test TableName method
			tableName := tt.metric.TableName()
			assert.Equal(t, "metrics", tableName)
		})
	}
}

func TestCredentialProfile(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		profile  CredentialProfile
		wantErr  bool
		validate func(CredentialProfile, *testing.T)
	}{
		{
			name: "valid credential profile",
			profile: CredentialProfile{
				ID:          1,
				Name:        "Test Profile",
				Description: "A test credential profile",
				Protocol:    "snmp",
				Payload:     "encrypted_payload",
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			wantErr: false,
			validate: func(cp CredentialProfile, t *testing.T) {
				assert.Equal(t, int64(1), cp.ID)
				assert.Equal(t, "Test Profile", cp.Name)
				assert.Equal(t, "A test credential profile", cp.Description)
				assert.Equal(t, "snmp", cp.Protocol)
				assert.Equal(t, "encrypted_payload", cp.Payload)
				assert.WithinDuration(t, now, cp.CreatedAt, time.Second)
				assert.WithinDuration(t, now, cp.UpdatedAt, time.Second)
				
				// Test GetID method
				assert.Equal(t, int64(1), cp.GetID())
			},
		},
		{
			name: "minimal credential profile",
			profile: CredentialProfile{
				ID:       2,
				Name:     "Minimal Profile",
				Protocol: "ssh",
				Payload:  "minimal_payload",
			},
			wantErr: false,
			validate: func(cp CredentialProfile, t *testing.T) {
				assert.Equal(t, int64(2), cp.ID)
				assert.Equal(t, "Minimal Profile", cp.Name)
				assert.Equal(t, "", cp.Description) // Default empty string
				assert.Equal(t, "ssh", cp.Protocol)
				assert.Equal(t, "minimal_payload", cp.Payload)
				// CreatedAt and UpdatedAt will have default values
			},
		},
		{
			name: "zero values credential profile",
			profile: CredentialProfile{
				ID:       0,
				Name:     "",
				Protocol: "",
				Payload:  "",
			},
			wantErr: false,
			validate: func(cp CredentialProfile, t *testing.T) {
				assert.Equal(t, int64(0), cp.ID)
				assert.Equal(t, "", cp.Name)
				assert.Equal(t, "", cp.Protocol)
				assert.Equal(t, "", cp.Payload)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(tt.profile, t)
			
			// Test TableName method
			tableName := tt.profile.TableName()
			assert.Equal(t, "credential_profiles", tableName)
		})
	}
}

func TestDiscoveryProfile(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		profile  DiscoveryProfile
		wantErr  bool
		validate func(DiscoveryProfile, *testing.T)
	}{
		{
			name: "valid discovery profile",
			profile: DiscoveryProfile{
				ID:                  1,
				Name:                "Test Discovery",
				Target:              "192.168.1.0/24",
				Port:                161,
				CredentialProfileID: 10,
				CredentialProfile: &CredentialProfile{
					ID:       10,
					Name:     "SNMP Profile",
					Protocol: "snmp",
					Payload:  "encrypted_data",
				},
				AutoProvision: true,
				AutoRun:       false,
				CreatedAt:     now,
				UpdatedAt:     now,
			},
			wantErr: false,
			validate: func(dp DiscoveryProfile, t *testing.T) {
				assert.Equal(t, int64(1), dp.ID)
				assert.Equal(t, "Test Discovery", dp.Name)
				assert.Equal(t, "192.168.1.0/24", dp.Target)
				assert.Equal(t, 161, dp.Port)
				assert.Equal(t, int64(10), dp.CredentialProfileID)
				assert.NotNil(t, dp.CredentialProfile)
				assert.Equal(t, int64(10), dp.CredentialProfile.ID)
				assert.Equal(t, "SNMP Profile", dp.CredentialProfile.Name)
				assert.Equal(t, true, dp.AutoProvision)
				assert.Equal(t, false, dp.AutoRun)
				assert.WithinDuration(t, now, dp.CreatedAt, time.Second)
				assert.WithinDuration(t, now, dp.UpdatedAt, time.Second)
				
				// Test GetID method
				assert.Equal(t, int64(1), dp.GetID())
			},
		},
		{
			name: "minimal discovery profile",
			profile: DiscoveryProfile{
				ID:                  2,
				Name:                "Minimal Discovery",
				Target:              "10.0.0.1",
				Port:                80,
				CredentialProfileID: 5,
			},
			wantErr: false,
			validate: func(dp DiscoveryProfile, t *testing.T) {
				assert.Equal(t, int64(2), dp.ID)
				assert.Equal(t, "Minimal Discovery", dp.Name)
				assert.Equal(t, "10.0.0.1", dp.Target)
				assert.Equal(t, 80, dp.Port)
				assert.Equal(t, int64(5), dp.CredentialProfileID)
				assert.Nil(t, dp.CredentialProfile) // Foreign key reference is nil when not populated
				assert.False(t, dp.AutoProvision)
				assert.False(t, dp.AutoRun)
			},
		},
		{
			name: "zero values discovery profile",
			profile: DiscoveryProfile{
				ID:                  0,
				Name:                "",
				Target:              "",
				Port:                0,
				CredentialProfileID: 0,
			},
			wantErr: false,
			validate: func(dp DiscoveryProfile, t *testing.T) {
				assert.Equal(t, int64(0), dp.ID)
				assert.Equal(t, "", dp.Name)
				assert.Equal(t, "", dp.Target)
				assert.Equal(t, 0, dp.Port)
				assert.Equal(t, int64(0), dp.CredentialProfileID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(tt.profile, t)
			
			// Test TableName method
			tableName := tt.profile.TableName()
			assert.Equal(t, "discovery_profiles", tableName)
		})
	}
}

func TestDevice(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		device   Device
		wantErr  bool
		validate func(Device, *testing.T)
	}{
		{
			name: "valid device",
			device: Device{
				ID:                 1,
				DiscoveryProfileID: 5,
				DiscoveryProfile: &DiscoveryProfile{
					ID:       5,
					Name:     "Test Discovery",
					Target:   "192.168.1.0/24",
					Port:     161,
				},
				IPAddress: "192.168.1.100",
				Port:      80,
				Status:    "active",
				CreatedAt: now,
				UpdatedAt: now,
			},
			wantErr: false,
			validate: func(d Device, t *testing.T) {
				assert.Equal(t, int64(1), d.ID)
				assert.Equal(t, int64(5), d.DiscoveryProfileID)
				assert.NotNil(t, d.DiscoveryProfile)
				assert.Equal(t, int64(5), d.DiscoveryProfile.ID)
				assert.Equal(t, "Test Discovery", d.DiscoveryProfile.Name)
				assert.Equal(t, "192.168.1.100", d.IPAddress)
				assert.Equal(t, 80, d.Port)
				assert.Equal(t, "active", d.Status)
				assert.WithinDuration(t, now, d.CreatedAt, time.Second)
				assert.WithinDuration(t, now, d.UpdatedAt, time.Second)
				
				// Test GetID method
				assert.Equal(t, int64(1), d.GetID())
			},
		},
		{
			name: "minimal device",
			device: Device{
				ID:                 2,
				DiscoveryProfileID: 3,
				IPAddress:          "10.0.0.1",
				Port:               443,
			},
			wantErr: false,
			validate: func(d Device, t *testing.T) {
				assert.Equal(t, int64(2), d.ID)
				assert.Equal(t, int64(3), d.DiscoveryProfileID)
				assert.Nil(t, d.DiscoveryProfile) // Foreign key reference is nil when not populated
				assert.Equal(t, "10.0.0.1", d.IPAddress)
				assert.Equal(t, 443, d.Port)
				assert.Equal(t, "new", d.Status) // Default status
			},
		},
		{
			name: "device with different status",
			device: Device{
				ID:                 3,
				DiscoveryProfileID: 4,
				IPAddress:          "172.16.0.50",
				Port:               22,
				Status:             "inactive",
			},
			wantErr: false,
			validate: func(d Device, t *testing.T) {
				assert.Equal(t, int64(3), d.ID)
				assert.Equal(t, int64(4), d.DiscoveryProfileID)
				assert.Equal(t, "172.16.0.50", d.IPAddress)
				assert.Equal(t, 22, d.Port)
				assert.Equal(t, "inactive", d.Status)
			},
		},
		{
			name: "zero values device",
			device: Device{
				ID:                 0,
				DiscoveryProfileID: 0,
				IPAddress:          "",
				Port:               0,
				Status:             "",
			},
			wantErr: false,
			validate: func(d Device, t *testing.T) {
				assert.Equal(t, int64(0), d.ID)
				assert.Equal(t, int64(0), d.DiscoveryProfileID)
				assert.Equal(t, "", d.IPAddress)
				assert.Equal(t, 0, d.Port)
				assert.Equal(t, "new", d.Status) // Default status
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(tt.device, t)
			
			// Test TableName method
			tableName := tt.device.TableName()
			assert.Equal(t, "devices", tableName)
		})
	}
}

func TestMonitor(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		monitor  Monitor
		wantErr  bool
		validate func(Monitor, *testing.T)
	}{
		{
			name: "valid monitor",
			monitor: Monitor{
				ID:                     1,
				Hostname:               "test-host",
				IPAddress:              "192.168.1.100",
				PluginID:               "snmp_plugin",
				Port:                   161,
				CredentialProfileID:    5,
				CredentialProfile: &CredentialProfile{
					ID:       5,
					Name:     "SNMP Credentials",
					Protocol: "snmp",
				},
				DiscoveryProfileID:     10,
				DiscoveryProfile: &DiscoveryProfile{
					ID:       10,
					Name:     "Discovery Profile",
					Target:   "192.168.1.0/24",
				},
				PollingIntervalSeconds: 30,
				Status:                 "active",
				CreatedAt:              now,
				UpdatedAt:              now,
			},
			wantErr: false,
			validate: func(m Monitor, t *testing.T) {
				assert.Equal(t, int64(1), m.ID)
				assert.Equal(t, "test-host", m.Hostname)
				assert.Equal(t, "192.168.1.100", m.IPAddress)
				assert.Equal(t, "snmp_plugin", m.PluginID)
				assert.Equal(t, 161, m.Port)
				assert.Equal(t, int64(5), m.CredentialProfileID)
				assert.Equal(t, int64(10), m.DiscoveryProfileID)
				assert.NotNil(t, m.CredentialProfile)
				assert.Equal(t, "SNMP Credentials", m.CredentialProfile.Name)
				assert.NotNil(t, m.DiscoveryProfile)
				assert.Equal(t, "Discovery Profile", m.DiscoveryProfile.Name)
				assert.Equal(t, 30, m.PollingIntervalSeconds)
				assert.Equal(t, "active", m.Status)
				assert.WithinDuration(t, now, m.CreatedAt, time.Second)
				assert.WithinDuration(t, now, m.UpdatedAt, time.Second)
				
				// Test GetID method
				assert.Equal(t, int64(1), m.GetID())
			},
		},
		{
			name: "minimal monitor",
			monitor: Monitor{
				ID:                     2,
				IPAddress:              "10.0.0.1",
				PluginID:               "ping_plugin",
				CredentialProfileID:    1,
				DiscoveryProfileID:     1,
				PollingIntervalSeconds: 60, // Default value
				Status:                 "active",
			},
			wantErr: false,
			validate: func(m Monitor, t *testing.T) {
				assert.Equal(t, int64(2), m.ID)
				assert.Equal(t, "", m.Hostname) // Optional field
				assert.Equal(t, "10.0.0.1", m.IPAddress)
				assert.Equal(t, "ping_plugin", m.PluginID)
				assert.Equal(t, 0, m.Port) // Default zero value
				assert.Equal(t, int64(1), m.CredentialProfileID)
				assert.Equal(t, int64(1), m.DiscoveryProfileID)
				assert.Nil(t, m.CredentialProfile) // Foreign key reference is nil when not populated
				assert.Nil(t, m.DiscoveryProfile)  // Foreign key reference is nil when not populated
				assert.Equal(t, 60, m.PollingIntervalSeconds) // Default value
				assert.Equal(t, "active", m.Status)
			},
		},
		{
			name: "monitor with different status",
			monitor: Monitor{
				ID:                  3,
				IPAddress:           "172.16.0.50",
				PluginID:            "http_plugin",
				CredentialProfileID: 2,
				DiscoveryProfileID:  2,
				Status:              "inactive",
			},
			wantErr: false,
			validate: func(m Monitor, t *testing.T) {
				assert.Equal(t, int64(3), m.ID)
				assert.Equal(t, "172.16.0.50", m.IPAddress)
				assert.Equal(t, "http_plugin", m.PluginID)
				assert.Equal(t, int64(2), m.CredentialProfileID)
				assert.Equal(t, int64(2), m.DiscoveryProfileID)
				assert.Equal(t, "inactive", m.Status)
			},
		},
		{
			name: "monitor with error status",
			monitor: Monitor{
				ID:                  4,
				IPAddress:           "192.168.100.1",
				PluginID:            "snmp_plugin",
				CredentialProfileID: 3,
				DiscoveryProfileID:  3,
				Status:              "error",
			},
			wantErr: false,
			validate: func(m Monitor, t *testing.T) {
				assert.Equal(t, int64(4), m.ID)
				assert.Equal(t, "192.168.100.1", m.IPAddress)
				assert.Equal(t, "snmp_plugin", m.PluginID)
				assert.Equal(t, int64(3), m.CredentialProfileID)
				assert.Equal(t, int64(3), m.DiscoveryProfileID)
				assert.Equal(t, "error", m.Status)
			},
		},
		{
			name: "zero values monitor",
			monitor: Monitor{
				ID:                  0,
				IPAddress:           "",
				PluginID:            "",
				CredentialProfileID: 0,
				DiscoveryProfileID:  0,
			},
			wantErr: false,
			validate: func(m Monitor, t *testing.T) {
				assert.Equal(t, int64(0), m.ID)
				assert.Equal(t, "", m.Hostname)
				assert.Equal(t, "", m.IPAddress)
				assert.Equal(t, "", m.PluginID)
				assert.Equal(t, 0, m.Port)
				assert.Equal(t, int64(0), m.CredentialProfileID)
				assert.Equal(t, int64(0), m.DiscoveryProfileID)
				assert.Equal(t, 0, m.PollingIntervalSeconds) // Zero value, but GORM default is 60
				assert.Equal(t, "active", m.Status)          // Default value, but GORM default is "active"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(tt.monitor, t)
			
			// Test TableName method
			tableName := tt.monitor.TableName()
			assert.Equal(t, "monitors", tableName)
		})
	}
}

func TestMetricQuery(t *testing.T) {
	startTime := time.Now().Add(-time.Hour)
	endTime := time.Now()

	tests := []struct {
		name     string
		query    MetricQuery
		wantErr  bool
		validate func(MetricQuery, *testing.T)
	}{
		{
			name: "valid metric query",
			query: MetricQuery{
				Path:  "cpu.total",
				Start: startTime,
				End:   endTime,
				Limit: 100,
			},
			wantErr: false,
			validate: func(mq MetricQuery, t *testing.T) {
				assert.Equal(t, "cpu.total", mq.Path)
				assert.WithinDuration(t, startTime, mq.Start, time.Second)
				assert.WithinDuration(t, endTime, mq.End, time.Second)
				assert.Equal(t, 100, mq.Limit)
			},
		},
		{
			name: "minimal metric query",
			query: MetricQuery{
				Start: startTime,
				End:   endTime,
			},
			wantErr: false,
			validate: func(mq MetricQuery, t *testing.T) {
				assert.Equal(t, "", mq.Path) // Default empty string
				assert.WithinDuration(t, startTime, mq.Start, time.Second)
				assert.WithinDuration(t, endTime, mq.End, time.Second)
				assert.Equal(t, 0, mq.Limit) // Default zero value
			},
		},
		{
			name: "metric query with limit only",
			query: MetricQuery{
				Limit: 50,
			},
			wantErr: false,
			validate: func(mq MetricQuery, t *testing.T) {
				assert.Equal(t, "", mq.Path)
				assert.True(t, mq.Start.IsZero()) // Default zero time
				assert.True(t, mq.End.IsZero())   // Default zero time
				assert.Equal(t, 50, mq.Limit)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(tt.query, t)
		})
	}
}

func TestEvent(t *testing.T) {
	testPayload := map[string]interface{}{"key": "value"}

	tests := []struct {
		name     string
		event    Event
		wantErr  bool
		validate func(Event, *testing.T)
	}{
		{
			name: "create event",
			event: Event{
				Type:    EventCreate,
				Payload: testPayload,
			},
			wantErr: false,
			validate: func(e Event, t *testing.T) {
				assert.Equal(t, EventCreate, e.Type)
				assert.Equal(t, testPayload, e.Payload)
			},
		},
		{
			name: "update event",
			event: Event{
				Type:    EventUpdate,
				Payload: "simple string payload",
			},
			wantErr: false,
			validate: func(e Event, t *testing.T) {
				assert.Equal(t, EventUpdate, e.Type)
				assert.Equal(t, "simple string payload", e.Payload)
			},
		},
		{
			name: "delete event",
			event: Event{
				Type:    EventDelete,
				Payload: 42,
			},
			wantErr: false,
			validate: func(e Event, t *testing.T) {
				assert.Equal(t, EventDelete, e.Type)
				assert.Equal(t, 42, e.Payload)
			},
		},
		{
			name: "anything event",
			event: Event{
				Type:    EventAnything,
				Payload: nil,
			},
			wantErr: false,
			validate: func(e Event, t *testing.T) {
				assert.Equal(t, EventAnything, e.Type)
				assert.Nil(t, e.Payload)
			},
		},
		{
			name: "zero values event",
			event: Event{
				Type:    "",
				Payload: nil,
			},
			wantErr: false,
			validate: func(e Event, t *testing.T) {
				assert.Equal(t, "", string(e.Type))
				assert.Nil(t, e.Payload)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(tt.event, t)
		})
	}
}

func TestConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		wantErr  bool
		validate func(Config, *testing.T)
	}{
		{
			name: "full config",
			config: Config{
				DBHost:                     "localhost",
				DBUser:                     "admin",
				DBPassword:                 "password",
				DBName:                     "nms_db",
				DBPort:                     "5432",
				ServerAddress:              ":8080",
				PluginsDir:                 "/opt/plugins",
				FpingPath:                  "/usr/bin/fping",
				DiscoveryIntervalSeconds:   30,
				PollingWorkerConcurrency:   5,
				DiscoveryWorkerConcurrency: 3,
				FpingWorkerConcurrency:     10,
				SchedulerTickIntervalSeconds: 1,
				FpingTimeoutMs:             1000,
				FpingRetryCount:            3,
				JWTSecret:                  "secret_key",
				TLSCertFile:                "/etc/certs/tls.crt",
				TLSKeyFile:                 "/etc/certs/tls.key",
			},
			wantErr: false,
			validate: func(c Config, t *testing.T) {
				assert.Equal(t, "localhost", c.DBHost)
				assert.Equal(t, "admin", c.DBUser)
				assert.Equal(t, "password", c.DBPassword)
				assert.Equal(t, "nms_db", c.DBName)
				assert.Equal(t, "5432", c.DBPort)
				assert.Equal(t, ":8080", c.ServerAddress)
				assert.Equal(t, "/opt/plugins", c.PluginsDir)
				assert.Equal(t, "/usr/bin/fping", c.FpingPath)
				assert.Equal(t, 30, c.DiscoveryIntervalSeconds)
				assert.Equal(t, 5, c.PollingWorkerConcurrency)
				assert.Equal(t, 3, c.DiscoveryWorkerConcurrency)
				assert.Equal(t, 10, c.FpingWorkerConcurrency)
				assert.Equal(t, 1, c.SchedulerTickIntervalSeconds)
				assert.Equal(t, 1000, c.FpingTimeoutMs)
				assert.Equal(t, 3, c.FpingRetryCount)
				assert.Equal(t, "secret_key", c.JWTSecret)
				assert.Equal(t, "/etc/certs/tls.crt", c.TLSCertFile)
				assert.Equal(t, "/etc/certs/tls.key", c.TLSKeyFile)
			},
		},
		{
			name: "minimal config",
			config: Config{
				DBHost:        "",
				DBUser:        "",
				DBPassword:    "",
				DBName:        "",
				DBPort:        "",
				ServerAddress: ":8080",
			},
			wantErr: false,
			validate: func(c Config, t *testing.T) {
				assert.Equal(t, "", c.DBHost)
				assert.Equal(t, "", c.DBUser)
				assert.Equal(t, "", c.DBPassword)
				assert.Equal(t, "", c.DBName)
				assert.Equal(t, "", c.DBPort)
				assert.Equal(t, ":8080", c.ServerAddress)
				// Other fields should have zero values
				assert.Equal(t, "", c.PluginsDir)
				assert.Equal(t, 0, c.DiscoveryIntervalSeconds)
				assert.Equal(t, 0, c.PollingWorkerConcurrency)
			},
		},
		{
			name:   "zero values config",
			config: Config{},
			wantErr: false,
			validate: func(c Config, t *testing.T) {
				assert.Equal(t, "", c.DBHost)
				assert.Equal(t, "", c.DBUser)
				assert.Equal(t, "", c.DBPassword)
				assert.Equal(t, "", c.DBName)
				assert.Equal(t, "", c.DBPort)
				assert.Equal(t, "", c.ServerAddress)
				assert.Equal(t, "", c.PluginsDir)
				assert.Equal(t, 0, c.DiscoveryIntervalSeconds)
				assert.Equal(t, 0, c.PollingWorkerConcurrency)
				assert.Equal(t, 0, c.DiscoveryWorkerConcurrency)
				assert.Equal(t, 0, c.FpingWorkerConcurrency)
				assert.Equal(t, 0, c.SchedulerTickIntervalSeconds)
				assert.Equal(t, 0, c.FpingTimeoutMs)
				assert.Equal(t, 0, c.FpingRetryCount)
				assert.Equal(t, "", c.JWTSecret)
				assert.Equal(t, "", c.TLSCertFile)
				assert.Equal(t, "", c.TLSKeyFile)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(tt.config, t)
		})
	}
}