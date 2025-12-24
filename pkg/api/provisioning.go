package api

import (
	"net/http"
	"strconv"

	"nms/pkg/models"

	"github.com/gin-gonic/gin"
)

// RunDiscoveryHandler publishes a event to trigger discovery (zero repo deps)
func RunDiscoveryHandler(eventChan chan<- models.Event) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			respondError(c, http.StatusBadRequest, "invalid id")
			return
		}

		// Publish command event
		eventChan <- models.Event{
			Type: models.EventTriggerDiscovery,
			Payload: &models.DiscoveryTriggerEvent{
				DiscoveryProfileID: id,
			},
		}

		c.JSON(http.StatusAccepted, gin.H{
			"message":    "discovery trigger queued",
			"profile_id": id,
		})
	}
}

// ProvisionRequest represents the request body for device provisioning
type ProvisionRequest struct {
	PollingIntervalSeconds int   `json:"polling_interval_seconds"`
	CredentialProfileID    int64 `json:"credential_profile_id" binding:"required"`
}

// ProvisionDeviceHandler publishes a command event to provision a device (zero repo deps)
func ProvisionDeviceHandler(provisionCh chan<- models.Event) gin.HandlerFunc {
	return func(context *gin.Context) {
		id, err := strconv.ParseInt(context.Param("id"), 10, 64)
		if err != nil {
			respondError(context, http.StatusBadRequest, "invalid device id")
			return
		}

		// Parse request body
		var req ProvisionRequest
		if err := context.ShouldBindJSON(&req); err != nil {
			respondError(context, http.StatusBadRequest, err.Error())
			return
		}

		// Default polling interval to 60 if not provided or invalid
		if req.PollingIntervalSeconds <= 0 {
			respondError(context, http.StatusBadRequest, "invalid poll interval")
			return
		}

		// Publish command event
		provisionCh <- models.Event{
			Type: models.EventProvisionDevice,
			Payload: &models.DeviceProvisionEvent{
				DeviceID:               id,
				CredentialProfileID:    req.CredentialProfileID,
				PollingIntervalSeconds: req.PollingIntervalSeconds,
			},
		}

		context.JSON(http.StatusAccepted, gin.H{
			"message":   "monitor provisioning queued",
			"device_id": id,
		})
	}
}
