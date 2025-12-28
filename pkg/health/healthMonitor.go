package health

import (
	"context"
	"log/slog"
	"time"

	"nms/pkg/models"
)

// FailureRecord tracks failure state for a single device.
type FailureRecord struct {
	LastTime time.Time
	Count    int
}

// HealthMonitor tracks device failures and deactivates devices that exceed the threshold.
// It is fully decoupled from other services - only communicates via channels.
type HealthMonitor struct {
	failures      map[int64]FailureRecord
	failureChan   <-chan models.Event   // Input: failure events (EventDeviceFailure)
	entityReqChan chan<- models.Request // Output: deactivation requests to EntityService
	window        time.Duration
	threshold     int
}

// NewHealthMonitor creates a new HealthMonitor instance.
func NewHealthMonitor(
	failureChan <-chan models.Event,
	entityReqChan chan<- models.Request,
	windowMin int,
	threshold int,
) *HealthMonitor {
	return &HealthMonitor{
		failures:      make(map[int64]FailureRecord),
		failureChan:   failureChan,
		entityReqChan: entityReqChan,
		window:        time.Duration(windowMin) * time.Minute,
		threshold:     threshold,
	}
}

// Run starts the health monitor's main loop.
func (hm *HealthMonitor) Run(ctx context.Context) {
	slog.Info("Starting health monitor", "component", "HealthMonitor", "window", hm.window.String(), "threshold", hm.threshold)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping health monitor", "component", "HealthMonitor")
			return
		case event := <-hm.failureChan:
			if event.Type != models.EventDeviceFailure {
				continue // Ignore non-failure events
			}
			if payload, ok := event.Payload.(*models.DeviceFailureEvent); ok {
				hm.handleFailure(payload)
			}
		}
	}
}

// handleFailure processes a failure event and updates the failure count.
func (hm *HealthMonitor) handleFailure(event *models.DeviceFailureEvent) {
	record := hm.failures[event.DeviceID]

	if event.Timestamp.Sub(record.LastTime) < hm.window {
		// Within window: increment count
		record.Count++
		slog.Debug("Failure count increased",
			"component", "HealthMonitor",
			"device_id", event.DeviceID,
			"reason", event.Reason,
			"count", record.Count,
			"threshold", hm.threshold,
		)

		if record.Count >= hm.threshold {
			slog.Warn("Device exceeded failure threshold, deactivating",
				"component", "HealthMonitor",
				"device_id", event.DeviceID,
				"count", record.Count,
			)
			hm.deactivateDevice(event.DeviceID)
			delete(hm.failures, event.DeviceID) // Clean up after deactivation
			return
		}
	} else {
		// Outside window: reset count to 1
		record.Count = 1
		slog.Debug("Failure window reset",
			"component", "HealthMonitor",
			"device_id", event.DeviceID,
			"reason", event.Reason,
		)
	}

	record.LastTime = event.Timestamp
	hm.failures[event.DeviceID] = record
}

// deactivateDevice sends a deactivation request to EntityService.
func (hm *HealthMonitor) deactivateDevice(deviceID int64) {
	replyCh := make(chan models.Response, 1)
	hm.entityReqChan <- models.Request{
		Operation:  models.OpDeactivateDevice,
		EntityType: "Device",
		ID:         deviceID,
		ReplyCh:    replyCh,
	}

	// Wait for response (non-blocking in terms of other failures)
	go func() {
		resp := <-replyCh
		if resp.Error != nil {
			slog.Error("Failed to deactivate device",
				"component", "HealthMonitor",
				"device_id", deviceID,
				"error", resp.Error,
			)
		} else {
			slog.Info("Device deactivated successfully",
				"component", "HealthMonitor",
				"device_id", deviceID,
			)
		}
	}()
}
