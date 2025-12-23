package persistence

import (
	"context"
	"fmt"
	"log/slog"

	"nms/pkg/database"
	"nms/pkg/models"
	"nms/pkg/plugin"

	"gorm.io/gorm"
)

// sendEvent sends an event to a channel without blocking.
// If the channel is full, it logs a warning and drops the event.
func sendEvent(ch chan<- models.Event, event models.Event) {
	select {
	case ch <- event:
	default:
		slog.Warn("Channel full, dropping event", "component", "EntityService", "event_type", event.Type)
	}
}

// EntityService handles all entity CRUD operations, discovery provisioning, and commands.
type EntityService struct {
	// Input channels
	discoveryResults <-chan plugin.Result
	commands         <-chan models.Event
	requests         <-chan models.Request

	// Database
	db *gorm.DB

	// Repositories
	credentialRepo       database.Repository[models.CredentialProfile]
	monitorRepo          database.Repository[models.Monitor]
	deviceRepo           database.Repository[models.Device]
	discoveryProfileRepo database.Repository[models.DiscoveryProfile]

	// Event publishing channels
	discoveryProfileEvents chan<- models.Event
	monitorEvents          chan<- models.Event
	credentialEvents       chan<- models.Event
}

// NewEntityService creates a new entity writer service.
func NewEntityService(
	discoveryResults <-chan plugin.Result,
	commands <-chan models.Event,
	requests <-chan models.Request,
	db *gorm.DB,
	credentialRepo database.Repository[models.CredentialProfile],
	monitorRepo database.Repository[models.Monitor],
	deviceRepo database.Repository[models.Device],
	discoveryProfileRepo database.Repository[models.DiscoveryProfile],
	discoveryProfileEvents chan<- models.Event,
	monitorEvents chan<- models.Event,
	credentialEvents chan<- models.Event,
) *EntityService {
	return &EntityService{
		discoveryResults:       discoveryResults,
		commands:               commands,
		requests:               requests,
		db:                     db,
		credentialRepo:         credentialRepo,
		monitorRepo:            monitorRepo,
		deviceRepo:             deviceRepo,
		discoveryProfileRepo:   discoveryProfileRepo,
		discoveryProfileEvents: discoveryProfileEvents,
		monitorEvents:          monitorEvents,
		credentialEvents:       credentialEvents,
	}
}

// Run starts the entity writer's main loop.
func (writer *EntityService) Run(ctx context.Context) {
	slog.Info("Starting entity writer", "component", "EntityService")

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping entity writer", "component", "EntityService")
			return
		case result := <-writer.discoveryResults:
			writer.provisionFromDiscovery(ctx, result)
		case cmd := <-writer.commands:
			writer.handleCommand(ctx, cmd)
		case req := <-writer.requests:
			writer.handleCrudRequest(ctx, req)
		}
	}
}

// provisionFromDiscovery creates a device and monitor from a discovery result.
func (writer *EntityService) provisionFromDiscovery(ctx context.Context, result plugin.Result) {
	slog.Info("Provisioning device from discovery", "component", "EntityService", "hostname", result.Hostname, "target", result.Target)

	// Check if device already exists for this IP
	var existingDevice models.Device
	err := writer.db.WithContext(ctx).
		Where("ip_address = ?", result.Target).
		First(&existingDevice).Error

	if err == nil {
		slog.Debug("Device already exists", "component", "EntityService", "target", result.Target, "device_id", existingDevice.ID)
		return
	}

	// Get plugin ID from credential profile
	var pluginID string
	if cred, err := writer.credentialRepo.Get(ctx, result.CredentialProfileID); err == nil && cred != nil {
		pluginID = cred.Protocol
	}

	// Create Device record
	device := models.Device{
		DiscoveryProfileID: result.DiscoveryProfileID,
		Hostname:           result.Hostname,
		IPAddress:          result.Target,
		Port:               result.Port,
		Status:             "discovered",
	}

	createdDevice, err := writer.deviceRepo.Create(ctx, &device)
	if err != nil {
		slog.Error("Failed to create device", "component", "EntityService", "target", result.Target, "error", err)
		return
	}
	slog.Info("Created device", "component", "EntityService", "device_id", createdDevice.ID, "target", result.Target)

	// Create Monitor record
	monitor := models.Monitor{
		Hostname:            result.Hostname,
		IPAddress:           result.Target,
		PluginID:            pluginID,
		Port:                result.Port,
		CredentialProfileID: result.CredentialProfileID,
		DiscoveryProfileID:  result.DiscoveryProfileID,
		Status:              "active",
	}

	createdMonitor, err := writer.monitorRepo.Create(ctx, &monitor)
	if err != nil {
		slog.Error("Failed to create monitor", "component", "EntityService", "target", result.Target, "error", err)
		return
	}
	slog.Info("Created monitor", "component", "EntityService", "monitor_id", createdMonitor.ID, "hostname", result.Hostname)
}

// handleCommand processes manual provisioning commands.
func (writer *EntityService) handleCommand(ctx context.Context, event models.Event) {
	switch event.Type {
	case models.EventTriggerDiscovery:
		writer.triggerDiscovery(ctx, event)
	case models.EventProvisionDevice:
		writer.provisionDevice(ctx, event)
	default:
		slog.Warn("Ignoring unknown command type", "component", "EntityService", "type", event.Type)
	}
}

// triggerDiscovery fetches a discovery profile and publishes an update event to start discovery.
func (writer *EntityService) triggerDiscovery(ctx context.Context, event models.Event) {
	cmd, ok := event.Payload.(*models.DiscoveryTriggerCommand)
	if !ok {
		slog.Error("Invalid payload for EventTriggerDiscovery", "component", "EntityService")
		return
	}

	profile, err := writer.discoveryProfileRepo.Get(ctx, cmd.DiscoveryProfileID)
	if err != nil {
		slog.Error("Failed to fetch discovery profile", "component", "EntityService", "profile_id", cmd.DiscoveryProfileID, "error", err)
		return
	}

	go sendEvent(writer.discoveryProfileEvents, models.Event{
		Type:    models.EventUpdate,
		Payload: profile,
	})

	slog.Info("Triggered discovery for profile", "component", "EntityService", "profile_id", cmd.DiscoveryProfileID)
}

// provisionDevice fetches a device and creates a monitor for it.
func (writer *EntityService) provisionDevice(ctx context.Context, event models.Event) {
	cmd, ok := event.Payload.(*models.DeviceProvisionCommand)
	if !ok {
		slog.Error("Invalid payload for EventProvisionDevice", "component", "EntityService")
		return
	}

	device, err := writer.deviceRepo.Get(ctx, cmd.DeviceID)
	if err != nil {
		slog.Error("Failed to fetch device", "component", "EntityService", "device_id", cmd.DeviceID, "error", err)
		return
	}

	// Get plugin ID from credential profile
	var pluginID string
	if cred, err := writer.credentialRepo.Get(ctx, cmd.CredentialProfileID); err == nil && cred != nil {
		pluginID = cred.Protocol
	}

	monitor := &models.Monitor{
		Hostname:               device.Hostname,
		IPAddress:              device.IPAddress,
		PluginID:               pluginID,
		Port:                   device.Port,
		CredentialProfileID:    cmd.CredentialProfileID,
		DiscoveryProfileID:     device.DiscoveryProfileID,
		PollingIntervalSeconds: cmd.PollingIntervalSeconds,
		Status:                 "active",
	}

	go sendEvent(writer.monitorEvents, models.Event{
		Type:    models.EventCreate,
		Payload: monitor,
	})

	slog.Info("Provisioned monitor for device", "component", "EntityService", "device_id", cmd.DeviceID)
}

// handleCRUD is a generic CRUD handler that works with any repository type.
func handleCRUD[T any](
	ctx context.Context,
	req models.Request,
	repo database.Repository[T],
	eventCh chan<- models.Event,
) models.Response {
	var resp models.Response

	switch req.Operation {
	case models.OpList:
		data, err := repo.List(ctx)
		resp.Data, resp.Error = data, err

	case models.OpGet:
		data, err := repo.Get(ctx, req.ID)
		resp.Data, resp.Error = data, err

	case models.OpCreate:
		entity, ok := req.Payload.(*T)
		if !ok {
			resp.Error = fmt.Errorf("invalid payload type")
			return resp
		}
		data, err := repo.Create(ctx, entity)
		if err == nil && eventCh != nil {
			go sendEvent(eventCh, models.Event{Type: models.EventCreate, Payload: data})
		}
		resp.Data, resp.Error = data, err

	case models.OpUpdate:
		entity, ok := req.Payload.(*T)
		if !ok {
			resp.Error = fmt.Errorf("invalid payload type")
			return resp
		}
		data, err := repo.Update(ctx, req.ID, entity)
		if err == nil && eventCh != nil {
			go sendEvent(eventCh, models.Event{Type: models.EventUpdate, Payload: data})
		}
		resp.Data, resp.Error = data, err

	case models.OpDelete:
		if eventCh != nil {
			// Fetch entity before delete for event payload
			entity, _ := repo.Get(ctx, req.ID)
			err := repo.Delete(ctx, req.ID)
			if err == nil && entity != nil {
				go sendEvent(eventCh, models.Event{Type: models.EventDelete, Payload: entity})
			}
			resp.Error = err
		} else {
			resp.Error = repo.Delete(ctx, req.ID)
		}

	default:
		resp.Error = fmt.Errorf("unknown operation: %s", req.Operation)
	}

	return resp
}

// handleDiscoveryProfileCRUD handles DiscoveryProfile CRUD and enriches events with credential data.
func (writer *EntityService) handleDiscoveryProfileCRUD(ctx context.Context, req models.Request) models.Response {
	var resp models.Response

	switch req.Operation {
	case models.OpList:
		data, err := writer.discoveryProfileRepo.List(ctx)
		resp.Data, resp.Error = data, err

	case models.OpGet:
		data, err := writer.discoveryProfileRepo.Get(ctx, req.ID)
		resp.Data, resp.Error = data, err

	case models.OpCreate:
		entity, ok := req.Payload.(*models.DiscoveryProfile)
		if !ok {
			resp.Error = fmt.Errorf("invalid payload type")
			return resp
		}
		data, err := writer.discoveryProfileRepo.Create(ctx, entity)
		if err == nil {
			// Enrich with credential profile before publishing event
			if cred, credErr := writer.credentialRepo.Get(ctx, data.CredentialProfileID); credErr == nil {
				data.CredentialProfile = cred
			}
			go sendEvent(writer.discoveryProfileEvents, models.Event{Type: models.EventCreate, Payload: data})
		}
		resp.Data, resp.Error = data, err

	case models.OpUpdate:
		entity, ok := req.Payload.(*models.DiscoveryProfile)
		if !ok {
			resp.Error = fmt.Errorf("invalid payload type")
			return resp
		}
		data, err := writer.discoveryProfileRepo.Update(ctx, req.ID, entity)
		if err == nil {
			// Enrich with credential profile before publishing event
			if cred, credErr := writer.credentialRepo.Get(ctx, data.CredentialProfileID); credErr == nil {
				data.CredentialProfile = cred
			}
			go sendEvent(writer.discoveryProfileEvents, models.Event{Type: models.EventUpdate, Payload: data})
		}
		resp.Data, resp.Error = data, err

	case models.OpDelete:
		entity, _ := writer.discoveryProfileRepo.Get(ctx, req.ID)
		err := writer.discoveryProfileRepo.Delete(ctx, req.ID)
		if err == nil && entity != nil {
			go sendEvent(writer.discoveryProfileEvents, models.Event{Type: models.EventDelete, Payload: entity})
		}
		resp.Error = err

	default:
		resp.Error = fmt.Errorf("unknown operation: %s", req.Operation)
	}

	return resp
}

// handleCrudRequest routes CRUD operations to appropriate repositories.
func (writer *EntityService) handleCrudRequest(ctx context.Context, req models.Request) {
	var resp models.Response

	switch req.EntityType {
	case "CredentialProfile":
		resp = handleCRUD(ctx, req, writer.credentialRepo, writer.credentialEvents)
	case "Device":
		resp = handleCRUD(ctx, req, writer.deviceRepo, nil)
	case "Monitor":
		resp = handleCRUD(ctx, req, writer.monitorRepo, writer.monitorEvents)
	case "DiscoveryProfile":
		resp = writer.handleDiscoveryProfileCRUD(ctx, req)
	default:
		resp.Error = fmt.Errorf("unknown entity type: %s", req.EntityType)
	}

	req.ReplyCh <- resp
}
