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

// EntityService handles all entity CRUD operations, discovery provisioning, and eventsChan.
type EntityService struct {
	// Input channels
	discoveryResultsChan <-chan plugin.Result
	eventsChan           <-chan models.Event
	requestsChan         <-chan models.Request

	// Repositories
	credentialRepo       database.Repository[models.CredentialProfile]
	deviceRepo           database.Repository[models.Device]
	discoveryProfileRepo database.Repository[models.DiscoveryProfile]

	// Event publishing channels
	discoveryProfileEvents chan<- models.Event
	deviceEvents           chan<- models.Event
	credentialEvents       chan<- models.Event
}

// NewEntityService creates a new entity writer service.
func NewEntityService(
	discoveryResults <-chan plugin.Result,
	eventsChan <-chan models.Event,
	requests <-chan models.Request,
	db *gorm.DB,
	discoveryProfileEvents chan<- models.Event,
	deviceEvents chan<- models.Event,
	credentialEvents chan<- models.Event,
) *EntityService {
	return &EntityService{
		discoveryResultsChan:   discoveryResults,
		eventsChan:             eventsChan,
		requestsChan:           requests,
		credentialRepo:         database.NewGormRepository[models.CredentialProfile](db),
		deviceRepo:             database.NewGormRepository[models.Device](db),
		discoveryProfileRepo:   database.NewGormRepository[models.DiscoveryProfile](db),
		discoveryProfileEvents: discoveryProfileEvents,
		deviceEvents:           deviceEvents,
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
		case result := <-writer.discoveryResultsChan:
			writer.provisionFromDiscovery(ctx, result)
		case event := <-writer.eventsChan:
			writer.handleEvent(ctx, event)
		case req := <-writer.requestsChan:
			writer.handleCrudRequest(ctx, req)
		}
	}
}

// provisionFromDiscovery creates a device from a discovery result.
func (writer *EntityService) provisionFromDiscovery(ctx context.Context, result plugin.Result) {
	slog.Info("Provisioning device from discovery", "component", "EntityService", "hostname", result.Hostname, "target", result.Target)

	// Check if device already exists for this IP and Port
	existingDevice, err := writer.deviceRepo.GetByFields(ctx, map[string]any{
		"ip_address": result.Target,
		"port":       result.Port,
	})

	if err == nil && existingDevice != nil {
		slog.Debug("Device already exists", "component", "EntityService", "target", result.Target, "port", result.Port, "device_id", existingDevice.ID)
		return
	}

	// Get plugin ID from credential profile
	var pluginID string
	if cred, err := writer.credentialRepo.Get(ctx, result.CredentialProfileID); err == nil && cred != nil {
		pluginID = cred.Protocol
	}

	// Determine initial status based on AutoProvision
	profile, err := writer.discoveryProfileRepo.Get(ctx, result.DiscoveryProfileID)
	if err != nil {
		slog.Error("Could not fetch discovery profile to check AutoProvision flag", "component", "EntityService", "profile_id", result.DiscoveryProfileID, "error", err)
	}

	initialStatus := "discovered"
	if profile != nil && profile.AutoProvision {
		initialStatus = "active"
	}

	// Create Device record
	device := models.Device{
		Hostname:            result.Hostname,
		IPAddress:           result.Target,
		PluginID:            pluginID,
		Port:                result.Port,
		CredentialProfileID: result.CredentialProfileID,
		DiscoveryProfileID:  result.DiscoveryProfileID,
		Status:              initialStatus,
	}

	createdDevice, err := writer.deviceRepo.Create(ctx, &device)
	if err != nil {
		slog.Error("Failed to create device", "component", "EntityService", "target", result.Target, "error", err)
		return
	}

	if initialStatus == "active" {
		// Publish event so scheduler picks it up
		go sendEvent(writer.deviceEvents, models.Event{
			Type:    models.EventCreate,
			Payload: createdDevice,
		})
		slog.Info("Created active device (AutoProvision enabled)", "component", "EntityService", "device_id", createdDevice.ID, "hostname", result.Hostname)
	} else {
		slog.Info("Created discovered device (AutoProvision disabled)", "component", "EntityService", "device_id", createdDevice.ID, "hostname", result.Hostname)
	}
}

// handleEvent processes manual provisioning eventsChan.
func (writer *EntityService) handleEvent(ctx context.Context, event models.Event) {
	switch event.Type {
	case models.EventTriggerDiscovery:
		writer.triggerDiscovery(ctx, event)
	case models.EventActivateDevice:
		writer.activateDevice(ctx, event)
	default:
		slog.Error("Ignoring unknown command type", "component", "EntityService", "type", event.Type)
	}
}

// triggerDiscovery fetches a discovery profile and publishes an update event to start discovery.
func (writer *EntityService) triggerDiscovery(ctx context.Context, event models.Event) {
	cmd, ok := event.Payload.(*models.DiscoveryTriggerEvent)
	if !ok {
		slog.Error("Invalid payload for EventTriggerDiscovery", "component", "EntityService")
		return
	}

	profile, err := writer.discoveryProfileRepo.Get(ctx, cmd.DiscoveryProfileID)
	if err != nil {
		slog.Error("Failed to fetch discovery profile", "component", "EntityService", "profile_id", cmd.DiscoveryProfileID, "error", err)
		return
	}

	// Enrich with credential profile before publishing event
	if cred, credErr := writer.credentialRepo.Get(ctx, profile.CredentialProfileID); credErr == nil {
		profile.CredentialProfile = cred
	}

	go sendEvent(writer.discoveryProfileEvents, models.Event{
		Type:    models.EventRunDiscovery,
		Payload: profile,
	})

	slog.Info("Triggered discovery for profile", "component", "EntityService", "profile_id", cmd.DiscoveryProfileID)
}

// activateDevice activates a discovered device and sets its polling interval.
func (writer *EntityService) activateDevice(ctx context.Context, event models.Event) {
	cmd, ok := event.Payload.(*models.DeviceActivateEvent)
	if !ok {
		slog.Error("Invalid payload for EventActivateDevice", "component", "EntityService")
		return
	}

	device, err := writer.deviceRepo.Get(ctx, cmd.DeviceID)
	if err != nil {
		slog.Error("Failed to fetch device", "component", "EntityService", "device_id", cmd.DeviceID, "error", err)
		return
	}

	// Update device status and polling interval
	device.Status = "active"
	if cmd.PollingIntervalSeconds > 0 {
		device.PollingIntervalSeconds = cmd.PollingIntervalSeconds
	}

	updatedDevice, err := writer.deviceRepo.Update(ctx, cmd.DeviceID, device)
	if err != nil {
		slog.Error("Failed to update device", "component", "EntityService", "device_id", cmd.DeviceID, "error", err)
		return
	}

	go sendEvent(writer.deviceEvents, models.Event{
		Type:    models.EventUpdate,
		Payload: updatedDevice,
	})

	slog.Info("Activated device", "component", "EntityService", "device_id", cmd.DeviceID)
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

// handleDiscoveryProfileCRUD handles DiscoveryProfile CRUD and enriches eventsChan with credential data.
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
		resp = handleCRUD(ctx, req, writer.deviceRepo, writer.deviceEvents)
	case "DiscoveryProfile":
		resp = writer.handleDiscoveryProfileCRUD(ctx, req)
	default:
		resp.Error = fmt.Errorf("unknown entity type: %s", req.EntityType)
	}

	req.ReplyCh <- resp
}
