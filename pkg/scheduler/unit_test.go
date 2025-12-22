package scheduler

import (
	"nms/pkg/models"
	"testing"
	"time"
)

func TestLoadCache(t *testing.T) {
	monChan := make(chan models.Event, 10)
	credChan := make(chan models.Event, 10)
	outChan := make(chan []*models.Monitor, 10)
	s := NewScheduler(monChan, credChan, outChan, "/usr/bin/fping", 5, 500, 2)

	monitors := []*models.Monitor{
		{ID: 1, IPAddress: "127.0.0.1", PollingIntervalSeconds: 60},
		{ID: 2, IPAddress: "192.168.1.1", PollingIntervalSeconds: 30},
	}
	creds := []*models.CredentialProfile{
		{ID: 1, Name: "Default SNMP"},
	}

	s.LoadCache(monitors, creds)

	if len(s.monitors) != 2 {
		t.Errorf("expected 2 monitors, got %d", len(s.monitors))
	}
	if len(s.creds) != 1 {
		t.Errorf("expected 1 cred, got %d", len(s.creds))
	}

	if s.monitors[1].Monitor.IPAddress != "127.0.0.1" {
		t.Errorf("expected IP 127.0.0.1, got %s", s.monitors[1].Monitor.IPAddress)
	}
	if s.creds[1].Name != "Default SNMP" {
		t.Errorf("expected Name Default SNMP, got %s", s.creds[1].Name)
	}

	// Deadlines should be initialized near Now
	now := time.Now()
	diff := s.monitors[1].Deadline.Sub(now)
	if diff < -1*time.Second || diff > 1*time.Second {
		t.Errorf("deadline %v too far from now %v", s.monitors[1].Deadline, now)
	}
}

func TestProcessMonitorEvent(t *testing.T) {
	monChan := make(chan models.Event, 10)
	credChan := make(chan models.Event, 10)
	outChan := make(chan []*models.Monitor, 10)
	s := NewScheduler(monChan, credChan, outChan, "/usr/bin/fping", 5, 500, 2)

	// Test Create Monitor
	mon := &models.Monitor{ID: 10, IPAddress: "10.0.0.1", PollingIntervalSeconds: 10}
	s.processMonitorEvent(models.Event{
		Type:    models.EventCreate,
		Payload: mon,
	})

	if len(s.monitors) != 1 {
		t.Errorf("expected 1 monitor, got %d", len(s.monitors))
	}
	if s.monitors[10].Monitor.IPAddress != "10.0.0.1" {
		t.Errorf("expected IP 10.0.0.1, got %s", s.monitors[10].Monitor.IPAddress)
	}

	// Test Update Monitor
	monUpdated := &models.Monitor{ID: 10, IPAddress: "10.0.0.2", PollingIntervalSeconds: 20}
	s.processMonitorEvent(models.Event{
		Type:    models.EventUpdate,
		Payload: monUpdated,
	})

	if s.monitors[10].Monitor.IPAddress != "10.0.0.2" {
		t.Errorf("expected IP 10.0.0.2, got %s", s.monitors[10].Monitor.IPAddress)
	}

	// Test Delete Monitor
	s.processMonitorEvent(models.Event{
		Type:    models.EventDelete,
		Payload: mon,
	})
	if len(s.monitors) != 0 {
		t.Errorf("expected 0 monitors, got %d", len(s.monitors))
	}
}

func TestProcessCredentialEvent(t *testing.T) {
	monChan := make(chan models.Event, 10)
	credChan := make(chan models.Event, 10)
	outChan := make(chan []*models.Monitor, 10)
	s := NewScheduler(monChan, credChan, outChan, "/usr/bin/fping", 5, 500, 2)

	// Test Create Cred
	cred := &models.CredentialProfile{ID: 5, Name: "Admin"}
	s.processCredentialEvent(models.Event{
		Type:    models.EventCreate,
		Payload: cred,
	})
	if len(s.creds) != 1 {
		t.Errorf("expected 1 cred, got %d", len(s.creds))
	}
	if s.creds[5].Name != "Admin" {
		t.Errorf("expected Name Admin, got %s", s.creds[5].Name)
	}

	// Test Update Cred
	credUpdated := &models.CredentialProfile{ID: 5, Name: "SuperAdmin"}
	s.processCredentialEvent(models.Event{
		Type:    models.EventUpdate,
		Payload: credUpdated,
	})
	if s.creds[5].Name != "SuperAdmin" {
		t.Errorf("expected Name SuperAdmin, got %s", s.creds[5].Name)
	}

	// Test Delete Cred
	s.processCredentialEvent(models.Event{
		Type:    models.EventDelete,
		Payload: cred,
	})
	if len(s.creds) != 0 {
		t.Errorf("expected 0 creds, got %d", len(s.creds))
	}
}
