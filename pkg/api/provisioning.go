package api

import (
	"net/http"
	"strconv"
	"time"

	"nms/pkg/models"
)

// publishEvent publishes a command event with a context and deadline so a full
// or stopped event channel returns 503 instead of hanging the handler forever.
func publishEvent(r *http.Request, ch chan<- models.Event, event models.Event) error {
	select {
	case ch <- event:
		return nil
	case <-r.Context().Done():
		return r.Context().Err()
	case <-time.After(rpcTimeout):
		return errServiceUnavailable
	}
}

// RunDiscoveryHandler validates the discovery profile exists, then publishes a
// command event to trigger discovery.
func RunDiscoveryHandler(eventChan chan<- models.Event, crudReqCh chan<- models.Request) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid id")
			return
		}

		// Validate the profile exists before queueing the trigger.
		resp, err := doRequest(r, crudReqCh, models.Request{
			Operation:  models.OpGet,
			EntityType: "DiscoveryProfile",
			ID:         id,
			ReplyCh:    make(chan models.Response, 1),
		})
		if err != nil {
			respondRPCError(w, err)
			return
		}
		if resp.Error != nil {
			respondServiceError(w, models.OpGet, "DiscoveryProfile", resp.Error)
			return
		}

		// Publish command event
		if err := publishEvent(r, eventChan, models.Event{
			Type: models.EventTriggerDiscovery,
			Payload: &models.DiscoveryTriggerEvent{
				DiscoveryProfileID: id,
			},
		}); err != nil {
			respondRPCError(w, err)
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]any{
			"message":    "discovery trigger queued",
			"profile_id": id,
		})
	}
}

// ProvisionRequest represents the request body for device activation
type ProvisionRequest struct {
	PollingIntervalSeconds int `json:"polling_interval_seconds" binding:"required,min=60,max=3600"`
}

// ProvisionDeviceHandler publishes a command event to provision a discovered device.
func ProvisionDeviceHandler(provisionCh chan<- models.Event) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid device id")
			return
		}

		// Parse request body
		var req ProvisionRequest
		if err := jsonDecode(r, &req); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.PollingIntervalSeconds < 60 || req.PollingIntervalSeconds > 3600 {
			respondError(w, http.StatusBadRequest, "polling_interval_seconds must be between 60 and 3600")
			return
		}

		// Publish command event
		if err := publishEvent(r, provisionCh, models.Event{
			Type: models.EventProvisionDevice,
			Payload: &models.DeviceProvisionEvent{
				DeviceID:               id,
				PollingIntervalSeconds: req.PollingIntervalSeconds,
			},
		}); err != nil {
			respondRPCError(w, err)
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]any{
			"message":   "device provisioning queued",
			"device_id": id,
		})
	}
}
