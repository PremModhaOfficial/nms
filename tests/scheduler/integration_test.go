package scheduler_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"nms/pkg/models"
	"nms/pkg/scheduler"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockFpingScript is a bash script that simulates fping behavior.
// It echoes back IPs that are considered "reachable".
const mockFpingScript = `#!/bin/bash
# Mock fping behavior: print IPs that end in .1 or .10 as reachable
for arg in "$@"; do
    if [[ "$arg" == *".1" ]] || [[ "$arg" == *".10" ]]; then
        echo "$arg"
    fi
done
exit 0
`

func TestScheduler_Integration(t *testing.T) {
	// 1. Setup mock fping
	tmpDir, err := ioutil.TempDir("", "fping-mock")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockPath := filepath.Join(tmpDir, "fping")
	err = ioutil.WriteFile(mockPath, []byte(mockFpingScript), 0755)
	if err != nil {
		t.Fatalf("failed to write mock fping: %v", err)
	}

	// 2. Initialize Scheduler
	deviceEvents := make(chan models.Event, 10)
	credEvents := make(chan models.Event, 10)
	outChan := make(chan []*models.Device, 10)
	// Low tick interval for fast testing
	s := scheduler.NewScheduler(deviceEvents, credEvents, outChan, mockPath, 1, 100, 1)

	// 3. Load initial data
	devices := []*models.Device{
		{ID: 1, IPAddress: "192.168.1.1", PollingIntervalSeconds: 1, PluginID: "icmp", Port: 0, ShouldPing: true}, // Reachable
		{ID: 2, IPAddress: "192.168.1.5", PollingIntervalSeconds: 1, PluginID: "icmp", Port: 0, ShouldPing: true}, // Unreachable
		{ID: 3, IPAddress: "10.0.0.10", PollingIntervalSeconds: 2, PluginID: "snmp", Port: 161, ShouldPing: true}, // Reachable
	}
	creds := []*models.CredentialProfile{
		{ID: 1, Name: "Cred1"},
	}
	s.LoadCache(devices, creds)

	// 4. Run Scheduler
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go s.Run(ctx)

	// 5. Verify Output
	// We expect ID 1 and 3 to be dispatched because they are reachable according to mock script
	receivedIDs := make(map[int64]bool)

	totalExpected := 2 // ID 1 and 3

	timeout := time.After(3 * time.Second)

Loop:
	for {
		select {
		case batch := <-outChan:
			for _, d := range batch {
				fmt.Printf("Integration Test: Received Device ID %d\n", d.ID)
				receivedIDs[d.ID] = true
			}
			if len(receivedIDs) >= totalExpected {
				break Loop
			}
		case <-timeout:
			t.Errorf("timeout waiting for devices. received: %v", receivedIDs)
			break Loop
		}
	}

	if !receivedIDs[1] {
		t.Error("expected device ID 1 to be received")
	}
	if !receivedIDs[3] {
		t.Error("expected device ID 3 to be received")
	}
	if receivedIDs[2] {
		t.Error("did not expect device ID 2 to be received")
	}

	// 6. Test dynamic update via Event
	newDev := &models.Device{ID: 4, IPAddress: "172.16.0.1", PollingIntervalSeconds: 1, PluginID: "icmp"}
	deviceEvents <- models.Event{
		Type:    models.EventCreate,
		Payload: newDev,
	}

	// Wait for ID 4
	timeout = time.After(3 * time.Second)
Loop2:
	for {
		select {
		case batch := <-outChan:
			for _, d := range batch {
				if d.ID == 4 {
					receivedIDs[4] = true
					break Loop2
				}
			}
		case <-timeout:
			t.Error("timeout waiting for dynamic device ID 4")
			break Loop2
		}
	}
}
