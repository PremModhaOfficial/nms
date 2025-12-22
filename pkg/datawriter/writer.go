package datawriter

import (
	"context"
	"log"

	"nms/pkg/database"
	"nms/pkg/models"
	"nms/pkg/plugin"

	"gorm.io/gorm"
)

// Writer handles persistence of poll and discovery results.
type Writer struct {
	pollResults <-chan []plugin.Result
	discResults <-chan plugin.Result
	db          *gorm.DB
	deviceRepo  database.Repository[models.Device]
	monitorRepo database.Repository[models.Monitor]
}

// NewWriter creates a new data writer service.
func NewWriter(
	pollResults <-chan []plugin.Result,
	discResults <-chan plugin.Result,
	db *gorm.DB,
	deviceRepo database.Repository[models.Device],
	monitorRepo database.Repository[models.Monitor],
) *Writer {
	return &Writer{
		pollResults: pollResults,
		discResults: discResults,
		db:          db,
		deviceRepo:  deviceRepo,
		monitorRepo: monitorRepo,
	}
}

// Run starts the data writer's main loop.
func (w *Writer) Run(ctx context.Context) {
	log.Println("[DataWriter] Starting")

	for {
		select {
		case <-ctx.Done():
			log.Println("[DataWriter] Stopping")
			return
		case results := <-w.pollResults:
			w.writePollResults(ctx, results)
		case result := <-w.discResults:
			w.writeDiscoveryResult(ctx, result)
		}
	}
}

// writePollResults persists polling metrics to the database.
func (w *Writer) writePollResults(ctx context.Context, results []plugin.Result) {
	log.Printf("[DataWriter] Writing %d poll results", len(results))

	for _, result := range results {
		if result.Success {
			metric := models.Metric{
				MonitorID: result.MonitorID,
				Data:      result.Data,
			}
			if err := w.db.WithContext(ctx).Create(&metric).Error; err != nil {
				log.Printf("[DataWriter] Error saving metric for monitor %d: %v", result.MonitorID, err)
			} else {
				log.Printf("[DataWriter] Saved metric for monitor %d (size: %d bytes)", result.MonitorID, len(result.Data))
			}
		} else {
			log.Printf("[DataWriter] [%s:%d] Error: %s", result.Target, result.Port, result.Error)
		}
	}
}

// writeDiscoveryResult provisions a device and monitor from discovery.
func (w *Writer) writeDiscoveryResult(ctx context.Context, result plugin.Result) {
	log.Printf("[DataWriter] Provisioning device: %s (%s)", result.Hostname, result.Target)

	// 1. Check if device already exists for this IP
	var existingDevice models.Device
	err := w.db.WithContext(ctx).
		Where("ip_address = ?", result.Target).
		First(&existingDevice).Error

	if err == nil {
		log.Printf("[DataWriter] Device already exists for IP=%s (ID=%d)", result.Target, existingDevice.ID)
		return
	}

	// 2. Create Device record
	device := models.Device{
		DiscoveryProfileID: result.DiscoveryProfileID,
		IPAddress:          result.Target,
		Port:               result.Port,
		Status:             "discovered",
	}

	createdDevice, err := w.deviceRepo.Create(ctx, &device)
	if err != nil {
		log.Printf("[DataWriter] Failed to create device for %s: %v", result.Target, err)
		return
	}
	log.Printf("[DataWriter] Created device ID=%d for IP=%s", createdDevice.ID, result.Target)

	// 3. Create Monitor record
	monitor := models.Monitor{
		Hostname:            result.Hostname,
		IPAddress:           result.Target,
		PluginID:            "winrm", // Default plugin, matches discovery protocol
		Port:                result.Port,
		CredentialProfileID: result.CredentialProfileID,
		DiscoveryProfileID:  result.DiscoveryProfileID,
		Status:              "active",
	}

	createdMonitor, err := w.monitorRepo.Create(ctx, &monitor)
	if err != nil {
		log.Printf("[DataWriter] Failed to create monitor for %s: %v", result.Target, err)
		return
	}
	log.Printf("[DataWriter] Created monitor ID=%d for hostname=%s", createdMonitor.ID, result.Hostname)
}
