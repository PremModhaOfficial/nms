package monitorFailure

import (
	"context"
	"log/slog"
	"time"

	"nms/pkg/models"
	"nms/pkg/tracex"

	"go.opentelemetry.io/otel/attribute"
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
		case event, ok := <-failService.failureChan:
			if !ok {
				slog.Info("Failure channel closed, stopping health monitor", "component", "FailureService")
				return
			}
			if event.Type != models.EventDeviceFailure {
				continue // Ignore non-failure events
			}
			if payload, ok := event.Payload.(*models.DeviceFailureEvent); ok {
				// Continue the trace from the producer (scheduler/metrics), carried
				// on the event's TraceID/SpanID fields.
				fctx, fspan := tracex.Start(models.RemoteContext(event.TraceID, event.SpanID), "health", "health.recordFailure")
				fspan.SetAttributes(attribute.Int64("nms.device_id", payload.DeviceID))
				failService.handleFailure(fctx, payload)
				fspan.End()
			}
		}
	}
}

// handleFailure processes a failure event and updates the failure count.
func (failService *FailureService) handleFailure(ctx context.Context, event *models.DeviceFailureEvent) {
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
			failService.deactivateDevice(ctx, event.DeviceID)
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
// The request-reply exchange is bounded by ctx and models.RPCTimeout so a
// stalled EntityService cannot wedge the health monitor.
func (failService *FailureService) deactivateDevice(ctx context.Context, deviceID int64) {
	// One span per deactivation, child of the recordFailure span that triggered it.
	dctx, dspan := tracex.Start(ctx, "health", "health.deactivate")
	defer dspan.End()
	dspan.SetAttributes(attribute.Int64("nms.device_id", deviceID))

	replyCh := make(chan models.Response, 1)
	req := models.Request{
		Operation:  models.OpDeactivateDevice,
		EntityType: "Device",
		ID:         deviceID,
		ReplyCh:    replyCh,
	}
	// Forward the span context so EntityService's deactivate handler joins the trace.
	models.StampRequest(dctx, &req)
	resp, err := models.Call(dctx, failService.entityReqChan, req)

	if err != nil {
		slog.Error("Failed to deactivate device",
			"component", "FailureService",
			"device_id", deviceID,
			"error", err,
		)
	} else if resp.Error != nil {
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
}
