package scheduler

import (
	"nms/pkg/models"
	"testing"
	"time"
)

func TestLoadCache(t *testing.T) {
	outChan := make(chan []*models.Monitor, 10)
	s := NewScheduler(outChan, "/usr/bin/fping", 5, 500, 2)

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

func TestProcessEvent(t *testing.T) {
	outChan := make(chan []*models.Monitor, 10)
	s := NewScheduler(outChan, "/usr/bin/fping", 5, 500, 2)

	// Test Create Monitor
	mon := &models.Monitor{ID: 10, IPAddress: "10.0.0.1", PollingIntervalSeconds: 10}
	s.processEvent(models.Event{
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
	s.processEvent(models.Event{
		Type:    models.EventUpdate,
		Payload: monUpdated,
	})

	if s.monitors[10].Monitor.IPAddress != "10.0.0.2" {
		t.Errorf("expected IP 10.0.0.2, got %s", s.monitors[10].Monitor.IPAddress)
	}

	// Test Create Cred
	cred := &models.CredentialProfile{ID: 5, Name: "Admin"}
	s.processEvent(models.Event{
		Type:    models.EventCreate,
		Payload: cred,
	})
	if len(s.creds) != 1 {
		t.Errorf("expected 1 cred, got %d", len(s.creds))
	}
	if s.creds[5].Name != "Admin" {
		t.Errorf("expected Name Admin, got %s", s.creds[5].Name)
	}

	// Test Delete Monitor
	s.processEvent(models.Event{
		Type:    models.EventDelete,
		Payload: mon,
	})
	if len(s.monitors) != 0 {
		t.Errorf("expected 0 monitors, got %d", len(s.monitors))
	}

	// Test Delete Cred
	s.processEvent(models.Event{
		Type:    models.EventDelete,
		Payload: cred,
	})
	if len(s.creds) != 0 {
		t.Errorf("expected 0 creds, got %d", len(s.creds))
	}
}
