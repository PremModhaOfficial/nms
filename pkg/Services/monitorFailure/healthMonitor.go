package monitorFailure

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

// FailureService tracks device failures and deactivates devices that exceed the threshold.
// It is fully decoupled from other services - only communicates via channels.
type FailureService struct {
	failures      map[int64]FailureRecord
	failureChan   <-chan models.Event   // Input: failure events (EventDeviceFailure)
	entityReqChan chan<- models.Request // Output: deactivation requests to EntityService
	window        time.Duration
	threshold     int
}

// NewHealthMonitor creates a new FailureService instance.
func NewHealthMonitor(
	failureChan <-chan models.Event,
	entityReqChan chan<- models.Request,
	windowMin int,
	threshold int,
) *FailureService {
	return &FailureService{
		failures:      make(map[int64]FailureRecord),
		failureChan:   failureChan,
		entityReqChan: entityReqChan,
		window:        time.Duration(windowMin) * time.Minute,
		threshold:     threshold,
	}
}

// Run starts the health monitor's main loop.
func (failService *FailureService) Run(ctx context.Context) {
	slog.Info("Starting health monitor", "component", "FailureService", "window", failService.window.String(), "threshold", failService.threshold)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping health monitor", "component", "FailureService")
			return
		case event := <-failService.failureChan:
			if event.Type != models.EventDeviceFailure {
				continue // Ignore non-failure events
			}
			if payload, ok := event.Payload.(*models.DeviceFailureEvent); ok {
				failService.handleFailure(payload)
			}
		}
	}
}

// handleFailure processes a failure event and updates the failure count.
func (failService *FailureService) handleFailure(event *models.DeviceFailureEvent) {
	record := failService.failures[event.DeviceID]

	if event.Timestamp.Sub(record.LastTime) < failService.window {
		// Within window: increment count
		record.Count++
		slog.Debug("Failure count increased",
			"component", "FailureService",
			"device_id", event.DeviceID,
			"reason", event.Reason,
			"count", record.Count,
			"threshold", failService.threshold,
		)

		if record.Count >= failService.threshold {
			slog.Warn("Device exceeded failure threshold, deactivating",
				"component", "FailureService",
				"device_id", event.DeviceID,
				"count", record.Count,
			)
			failService.deactivateDevice(event.DeviceID)
			delete(failService.failures, event.DeviceID) // Clean up after deactivation
			return
		}
	} else {
		// Outside window: reset count to 1
		record.Count = 1
		slog.Debug("Failure window reset",
			"component", "FailureService",
			"device_id", event.DeviceID,
			"reason", event.Reason,
		)
	}

	record.LastTime = event.Timestamp
	failService.failures[event.DeviceID] = record
}

// deactivateDevice sends a deactivation request to EntityService.
func (failService *FailureService) deactivateDevice(deviceID int64) {
	replyCh := make(chan models.Response, 1)
	failService.entityReqChan <- models.Request{
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
				"component", "FailureService",
				"device_id", deviceID,
				"error", resp.Error,
			)
		} else {
			slog.Info("Device deactivated successfully",
				"component", "FailureService",
				"device_id", deviceID,
			)
		}
	}()
}
