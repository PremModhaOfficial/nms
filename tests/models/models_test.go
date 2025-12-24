package models_test

import (
	"encoding/json"
	"testing"
	"time"

	"nms/pkg/models"
)

func TestMetric_TableName(t *testing.T) {
	expected := "metrics"
	actual := models.Metric{}.TableName()
	if actual != expected {
		t.Errorf("Expected table name '%s', got '%s'", expected, actual)
	}
}

func TestMetricStruct(t *testing.T) {
	tests := []struct {
		name        string
		id          int64
		deviceID    int64
		data        json.RawMessage
		timestamp   time.Time
		expectError bool
	}{
		{
			name:        "Valid metric with valid data",
			id:          1,
			deviceID:    100,
			data:        json.RawMessage(`{"cpu": 50, "memory": 75}`),
			timestamp:   time.Now(),
			expectError: false,
		},
		{
			name:        "Metric with empty data",
			id:          2,
			deviceID:    101,
			data:        json.RawMessage(`{}`),
			timestamp:   time.Now(),
			expectError: false,
		},
		{
			name:        "Metric with nil data",
			id:          3,
			deviceID:    102,
			data:        nil,
			timestamp:   time.Now(),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metric := models.Metric{
				ID:        tt.id,
				DeviceID:  tt.deviceID,
				Data:      tt.data,
				Timestamp: tt.timestamp,
			}

			if metric.ID != tt.id {
				t.Errorf("Expected ID %d, got %d", tt.id, metric.ID)
			}
			if metric.DeviceID != tt.deviceID {
				t.Errorf("Expected DeviceID %d, got %d", tt.deviceID, metric.DeviceID)
			}
			if string(metric.Data) != string(tt.data) {
				t.Errorf("Expected Data %s, got %s", tt.data, metric.Data)
			}
			if !metric.Timestamp.Equal(tt.timestamp) {
				t.Errorf("Expected Timestamp %v, got %v", tt.timestamp, metric.Timestamp)
			}
		})
	}
}

func TestCredentialProfile_TableName(t *testing.T) {
	expected := "credential_profiles"
	actual := models.CredentialProfile{}.TableName()
	if actual != expected {
		t.Errorf("Expected table name '%s', got '%s'", expected, actual)
	}
}

func TestCredentialProfileStruct(t *testing.T) {
	tests := []struct {
		name        string
		id          int64
		nameField   string
		protocol    string
		payload     json.RawMessage
		expectError bool
	}{
		{
			name:      "Valid credential profile",
			id:        1,
			nameField: "Test Profile",
			protocol:  "ssh",
		},
		{
			name:        "Credential profile with minimal fields",
			id:          2,
			nameField:   "Another Profile",
			protocol:    "winrm",
			payload:     json.RawMessage(`{"other": "data"}`),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := models.CredentialProfile{
				ID:        tt.id,
				Name:      tt.nameField,
				Protocol:  tt.protocol,
				Payload:   tt.payload,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}

			if profile.ID != tt.id {
				t.Errorf("Expected ID %d, got %d", tt.id, profile.ID)
			}
			if profile.Name != tt.nameField {
				t.Errorf("Expected Name %s, got %s", tt.nameField, profile.Name)
			}
			if profile.Protocol != tt.protocol {
				t.Errorf("Expected Protocol %s, got %s", tt.protocol, profile.Protocol)
			}
			if string(profile.Payload) != string(tt.payload) {
				t.Errorf("Expected Payload %s, got %s", string(tt.payload), string(profile.Payload))
			}
		})
	}
}

func TestDiscoveryProfileStruct(t *testing.T) {
	tests := []struct {
		name                string
		id                  int64
		profileName         string
		target              string
		port                int
		credentialProfileID int64
		autoProvision       bool
		autoRun             bool
		expectError         bool
	}{
		{
			name:                "Valid discovery profile",
			id:                  1,
			profileName:         "Test Discovery",
			target:              "192.168.1.0/24",
			port:                22,
			credentialProfileID: 1,
			autoProvision:       true,
			autoRun:             false,
			expectError:         false,
		},
		{
			name:                "Discovery profile with auto run enabled",
			id:                  2,
			profileName:         "Auto Run Discovery",
			target:              "10.0.0.1",
			port:                80,
			credentialProfileID: 2,
			autoProvision:       false,
			autoRun:             true,
			expectError:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := models.DiscoveryProfile{
				ID:                  tt.id,
				Name:                tt.profileName,
				Target:              tt.target,
				Port:                tt.port,
				CredentialProfileID: tt.credentialProfileID,
				AutoProvision:       tt.autoProvision,
				AutoRun:             tt.autoRun,
				CreatedAt:           time.Now(),
				UpdatedAt:           time.Now(),
			}

			if profile.ID != tt.id {
				t.Errorf("Expected ID %d, got %d", tt.id, profile.ID)
			}
			if profile.Name != tt.profileName {
				t.Errorf("Expected Name %s, got %s", tt.profileName, profile.Name)
			}
			if profile.Target != tt.target {
				t.Errorf("Expected Target %s, got %s", tt.target, profile.Target)
			}
			if profile.Port != tt.port {
				t.Errorf("Expected Port %d, got %d", tt.port, profile.Port)
			}
			if profile.CredentialProfileID != tt.credentialProfileID {
				t.Errorf("Expected CredentialProfileID %d, got %d", tt.credentialProfileID, profile.CredentialProfileID)
			}
			if profile.AutoProvision != tt.autoProvision {
				t.Errorf("Expected AutoProvision %t, got %t", tt.autoProvision, profile.AutoProvision)
			}
			if profile.AutoRun != tt.autoRun {
				t.Errorf("Expected AutoRun %t, got %t", tt.autoRun, profile.AutoRun)
			}
		})
	}
}

func TestDeviceStruct(t *testing.T) {
	tests := []struct {
		name                   string
		id                     int64
		ipAddress              string
		pluginID               string
		port                   int
		credentialProfileID    int64
		discoveryProfileID     int64
		pollingIntervalSeconds int
		status                 string
		expectError            bool
	}{
		{
			name:                   "Valid device with active status",
			id:                     1,
			ipAddress:              "192.168.1.100",
			pluginID:               "ssh",
			port:                   22,
			credentialProfileID:    1,
			discoveryProfileID:     1,
			pollingIntervalSeconds: 60,
			status:                 "active",
			expectError:            false,
		},
		{
			name:                   "Device with inactive status",
			id:                     2,
			ipAddress:              "10.0.0.10",
			pluginID:               "winrm",
			port:                   5985,
			credentialProfileID:    2,
			discoveryProfileID:     2,
			pollingIntervalSeconds: 120,
			status:                 "inactive",
			expectError:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device := models.Device{
				ID:                     tt.id,
				IPAddress:              tt.ipAddress,
				PluginID:               tt.pluginID,
				Port:                   tt.port,
				CredentialProfileID:    tt.credentialProfileID,
				DiscoveryProfileID:     tt.discoveryProfileID,
				PollingIntervalSeconds: tt.pollingIntervalSeconds,
				Status:                 tt.status,
				CreatedAt:              time.Now(),
				UpdatedAt:              time.Now(),
			}

			if device.ID != tt.id {
				t.Errorf("Expected ID %d, got %d", tt.id, device.ID)
			}
			if device.IPAddress != tt.ipAddress {
				t.Errorf("Expected IPAddress %s, got %s", tt.ipAddress, device.IPAddress)
			}
			if device.PluginID != tt.pluginID {
				t.Errorf("Expected PluginID %s, got %s", tt.pluginID, device.PluginID)
			}
			if device.Port != tt.port {
				t.Errorf("Expected Port %d, got %d", tt.port, device.Port)
			}
			if device.CredentialProfileID != tt.credentialProfileID {
				t.Errorf("Expected CredentialProfileID %d, got %d", tt.credentialProfileID, device.CredentialProfileID)
			}
			if device.DiscoveryProfileID != tt.discoveryProfileID {
				t.Errorf("Expected DiscoveryProfileID %d, got %d", tt.discoveryProfileID, device.DiscoveryProfileID)
			}
			if device.PollingIntervalSeconds != tt.pollingIntervalSeconds {
				t.Errorf("Expected PollingIntervalSeconds %d, got %d", tt.pollingIntervalSeconds, device.PollingIntervalSeconds)
			}
			if device.Status != tt.status {
				t.Errorf("Expected Status %s, got %s", tt.status, device.Status)
			}
		})
	}
}

func TestTableNameMethods(t *testing.T) {
	tests := []struct {
		name     string
		table    string
		expected string
	}{
		{
			name:     "CredentialProfile table name",
			table:    models.CredentialProfile{}.TableName(),
			expected: "credential_profiles",
		},
		{
			name:     "DiscoveryProfile table name",
			table:    models.DiscoveryProfile{}.TableName(),
			expected: "discovery_profiles",
		},
		{
			name:     "Device table name",
			table:    models.Device{}.TableName(),
			expected: "devices",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.table != tt.expected {
				t.Errorf("Expected table name '%s', got '%s'", tt.expected, tt.table)
			}
		})
	}
}

func TestGetIDMethods(t *testing.T) {
	tests := []struct {
		name     string
		entityID int64
	}{
		{
			name:     "CredentialProfile GetID",
			entityID: 100,
		},
		{
			name:     "DiscoveryProfile GetID",
			entityID: 200,
		},
		{
			name:     "Device GetID",
			entityID: 400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switch t.Name() {
			case "TestGetIDMethods/credentialProfile_GetID":
				cp := models.CredentialProfile{ID: tt.entityID}
				if cp.GetID() != tt.entityID {
					t.Errorf("Expected GetID() to return %d, got %d", tt.entityID, cp.GetID())
				}
			case "TestGetIDMethods/discoveryProfile_GetID":
				dp := models.DiscoveryProfile{ID: tt.entityID}
				if dp.GetID() != tt.entityID {
					t.Errorf("Expected GetID() to return %d, got %d", tt.entityID, dp.GetID())
				}
			case "TestGetIDMethods/device_GetID":
				d := models.Device{ID: tt.entityID}
				if d.GetID() != tt.entityID {
					t.Errorf("Expected GetID() to return %d, got %d", tt.entityID, d.GetID())
				}
			}
		})
	}
}
