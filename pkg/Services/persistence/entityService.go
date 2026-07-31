package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"nms/pkg/database"
	"nms/pkg/models"
	"nms/pkg/plugin"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
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

	// In-memory caches for fast lookups (no DB round-trips)
	deviceCache     map[int64]*models.Device
	credentialCache map[int64]*models.CredentialProfile
	cacheMu         sync.RWMutex
}

// NewEntityService creates a new entity writer service.
func NewEntityService(
	discoveryResults <-chan plugin.Result,
	eventsChan <-chan models.Event,
	requests <-chan models.Request,
	db *sqlx.DB,
	discoveryProfileEvents chan<- models.Event,
	deviceEvents chan<- models.Event,
) *EntityService {
	return &EntityService{
		discoveryResultsChan:   discoveryResults,
		eventsChan:             eventsChan,
		requestsChan:           requests,
		credentialRepo:         database.NewSqlxRepository[models.CredentialProfile](db),
		deviceRepo:             database.NewSqlxRepository[models.Device](db),
		discoveryProfileRepo:   database.NewSqlxRepository[models.DiscoveryProfile](db),
		discoveryProfileEvents: discoveryProfileEvents,
		deviceEvents:           deviceEvents,
		deviceCache:            make(map[int64]*models.Device),
		credentialCache:        make(map[int64]*models.CredentialProfile),
	}
}

// Run starts the entity writer's main loop.
func (writer *EntityService) Run(ctx context.Context) {
	slog.Info("Starting entity writer", "component", "EntityService")

	// Reconcile caches periodically so devices/credentials added or removed by
	// direct DB writes (not via the API) are eventually reflected in memory.
	backfillCtx, cancelBackfill := context.WithCancel(ctx)
	defer cancelBackfill()
	go writer.cacheBackfill(backfillCtx, 5*time.Minute)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping entity writer", "component", "EntityService")
			return
		case result := <-writer.discoveryResultsChan:
			writer.safe("provisionFromDiscovery", func() { writer.provisionFromDiscovery(ctx, result) })
		case event := <-writer.eventsChan:
			writer.safe("handleEvent", func() { writer.handleEvent(ctx, event) })
		case req := <-writer.requestsChan:
			writer.safe("handleCrudRequest", func() { writer.handleCrudRequest(ctx, req) })
		}
	}
}

// safe runs fn with panic containment so a single bad input cannot kill the
// state-owning goroutine (which would freeze every caller on requestsChan).
func (writer *EntityService) safe(op string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic recovered in entity service", "component", "EntityService", "op", op, "error", r)
		}
	}()
	fn()
}

// isNotFound reports whether err means "no rows" as opposed to a real failure.
func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505), replacing brittle string matching.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
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
	if err != nil && !isNotFound(err) {
		// A real DB failure must not be mistaken for "no duplicate".
		slog.Error("Failed to check existing device", "component", "EntityService", "target", result.Target, "port", result.Port, "error", err)
		return
	}

	// Get plugin ID from credential profile. A missing credential must not
	// silently create a device that the poller can never poll.
	cred, credErr := writer.credentialRepo.Get(ctx, result.CredentialProfileID)
	if credErr != nil {
		slog.Error("Failed to fetch credential profile, skipping provisioning", "component", "EntityService", "credential_id", result.CredentialProfileID, "error", credErr)
		return
	}
	pluginID := cred.Protocol

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
		// Handle race condition: check if error is due to unique constraint (IP + Port)
		if isUniqueViolation(err) {
			slog.Debug("Device already exists (caught by DB constraint)", "component", "EntityService", "target", result.Target, "port", result.Port)
			return
		}
		slog.Error("Failed to create device", "component", "EntityService", "target", result.Target, "error", err)
		return
	}

	// Update cache with newly created device
	writer.updateDeviceCache(models.OpCreate, createdDevice)

	if initialStatus == "active" {
		// Publish event so scheduler picks it up
		sendEvent(writer.deviceEvents, models.Event{
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
	case models.EventProvisionDevice:
		writer.provisionDevice(ctx, event)
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

	sendEvent(writer.discoveryProfileEvents, models.Event{
		Type:    models.EventRunDiscovery,
		Payload: profile,
	})

	slog.Info("Triggered discovery for profile", "component", "EntityService", "profile_id", cmd.DiscoveryProfileID)
}

// provisionDevice provisions a discovered device and sets its polling interval.
func (writer *EntityService) provisionDevice(ctx context.Context, event models.Event) {
	cmd, ok := event.Payload.(*models.DeviceProvisionEvent)
	if !ok {
		slog.Error("Invalid payload for EventProvisionDevice", "component", "EntityService")
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

	// Update cache with activated device
	writer.updateDeviceCache(models.OpUpdate, updatedDevice)

	sendEvent(writer.deviceEvents, models.Event{
		Type:    models.EventUpdate,
		Payload: updatedDevice,
	})

	slog.Info("Provisioned device", "component", "EntityService", "device_id", cmd.DeviceID)
}

// handleCRUD is a generic CRUD handler that works with any repository type.
func handleCRUD[T models.TableNamer](
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
			sendEvent(eventCh, models.Event{Type: models.EventCreate, Payload: data})
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
			sendEvent(eventCh, models.Event{Type: models.EventUpdate, Payload: data})
		}
		resp.Data, resp.Error = data, err

	case models.OpDelete:
		if eventCh != nil {
			// Fetch entity before delete for event payload
			entity, _ := repo.Get(ctx, req.ID)
			err := repo.Delete(ctx, req.ID)
			if err == nil && entity != nil {
				sendEvent(eventCh, models.Event{Type: models.EventDelete, Payload: entity})
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

	if profile, ok := req.Payload.(*models.DiscoveryProfile); ok {
		if err := validateDiscoveryProfile(profile, req.Operation); err != nil {
			return models.Response{Error: err}
		}
	}

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
			sendEvent(writer.discoveryProfileEvents, models.Event{Type: models.EventCreate, Payload: data})
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
			sendEvent(writer.discoveryProfileEvents, models.Event{Type: models.EventUpdate, Payload: data})
		}
		resp.Data, resp.Error = data, err

	case models.OpDelete:
		entity, _ := writer.discoveryProfileRepo.Get(ctx, req.ID)
		err := writer.discoveryProfileRepo.Delete(ctx, req.ID)
		if err == nil && entity != nil {
			sendEvent(writer.discoveryProfileEvents, models.Event{Type: models.EventDelete, Payload: entity})
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

	switch req.Operation {
	case models.OpGetBatch:
		resp = writer.handleGetBatch(req)
	case models.OpGetCredential:
		resp = writer.handleGetCredential(req)
	case models.OpDeactivateDevice:
		resp = writer.handleDeactivateDevice(ctx, req.ID)
	default:
		// Standard CRUD operations
		switch req.EntityType {
		case "CredentialProfile":
			resp = writer.handleCredentialCRUD(ctx, req)
		case "Device":
			resp = writer.handleDeviceCRUD(ctx, req)
		case "DiscoveryProfile":
			resp = writer.handleDiscoveryProfileCRUD(ctx, req)
		default:
			resp.Error = fmt.Errorf("unknown entity type: %s", req.EntityType)
		}
	}

	// Reply must always be delivered; a missing/blocked reply channel wedges
	// the caller forever. Guard the send so shutdown cannot stall the loop.
	select {
	case <-ctx.Done():
		slog.Warn("Context cancelled before replying", "component", "EntityService", "op", req.Operation)
	case req.ReplyCh <- resp:
	}
}

// handleCredentialCRUD handles CRUD for credentials and updates cache
func (writer *EntityService) handleCredentialCRUD(ctx context.Context, req models.Request) models.Response {
	if cred, ok := req.Payload.(*models.CredentialProfile); ok {
		switch req.Operation {
		case models.OpCreate:
			// Payload is validated at the service boundary (the model no longer
			// marks it binding:"required" so updates can omit it).
			if strings.TrimSpace(cred.Name) == "" {
				return models.Response{Error: fmt.Errorf("name cannot be empty or whitespace-only")}
			}
			if strings.TrimSpace(cred.Protocol) == "" {
				return models.Response{Error: fmt.Errorf("protocol cannot be empty or whitespace-only")}
			}
			if cred.Payload == "" {
				return models.Response{Error: fmt.Errorf("payload is required on create")}
			}
		case models.OpUpdate:
			if strings.TrimSpace(cred.Name) == "" {
				return models.Response{Error: fmt.Errorf("name cannot be empty or whitespace-only")}
			}
		}
	}

	resp := handleCRUD(ctx, req, writer.credentialRepo, nil) // No event channel - credentials don't need broadcast
	if resp.Error == nil {
		writer.updateCredentialCache(req.Operation, resp.Data)
	}
	return resp
}

// handleDeviceCRUD handles CRUD for devices and updates cache
func (writer *EntityService) handleDeviceCRUD(ctx context.Context, req models.Request) models.Response {
	if device, ok := req.Payload.(*models.Device); ok {
		switch req.Operation {
		case models.OpCreate:
			// Require valid profile IDs on create
			if device.CredentialProfileID < 1 {
				return models.Response{Error: fmt.Errorf("credential_profile_id is required and must be >= 1")}
			}
			if device.DiscoveryProfileID < 1 {
				return models.Response{Error: fmt.Errorf("discovery_profile_id is required and must be >= 1")}
			}
			if strings.TrimSpace(device.IPAddress) == "" {
				return models.Response{Error: fmt.Errorf("ip_address is required")}
			}
			if strings.TrimSpace(device.PluginID) == "" {
				return models.Response{Error: fmt.Errorf("plugin_id is required")}
			}
			if err := validateDeviceFields(device); err != nil {
				return models.Response{Error: err}
			}
		case models.OpUpdate:
			// Fail-fast: credential_profile_id and discovery_profile_id are immutable
			if device.CredentialProfileID != 0 || device.DiscoveryProfileID != 0 {
				return models.Response{Error: fmt.Errorf("credential_profile_id and discovery_profile_id are immutable after creation")}
			}
			if err := validateDeviceFields(device); err != nil {
				return models.Response{Error: err}
			}
		}
	}

	resp := handleCRUD(ctx, req, writer.deviceRepo, writer.deviceEvents)
	if resp.Error == nil {
		writer.updateDeviceCache(req.Operation, resp.Data)
	}
	return resp
}

// validateDeviceFields enforces the ranges gin's binding tags previously
// checked: port 1-65535 (0 = default), polling interval 60-3600, IP format,
// and status enum. Zero/empty values are allowed (partial updates).
func validateDeviceFields(device *models.Device) error {
	if device.Port != 0 && (device.Port < 1 || device.Port > 65535) {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if device.PollingIntervalSeconds != 0 && (device.PollingIntervalSeconds < 60 || device.PollingIntervalSeconds > 3600) {
		return fmt.Errorf("polling_interval_seconds must be between 60 and 3600")
	}
	if device.IPAddress != "" && net.ParseIP(device.IPAddress) == nil {
		return fmt.Errorf("ip_address must be a valid IP address")
	}
	switch device.Status {
	case "", "discovered", "active", "inactive", "error":
	default:
		return fmt.Errorf("invalid status %q", device.Status)
	}
	return nil
}

// validateDiscoveryProfile enforces the required fields gin's binding tags
// previously checked on create and update.
func validateDiscoveryProfile(profile *models.DiscoveryProfile, op string) error {
	if strings.TrimSpace(profile.Name) == "" {
		return fmt.Errorf("name cannot be empty or whitespace-only")
	}
	if strings.TrimSpace(profile.Target) == "" {
		return fmt.Errorf("target cannot be empty or whitespace-only")
	}
	if profile.Port < 1 || profile.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

// updateDeviceCache updates the in-memory device cache based on CRUD operation
func (writer *EntityService) updateDeviceCache(op string, data interface{}) {
	device, ok := data.(*models.Device)
	if !ok {
		return
	}

	writer.cacheMu.Lock()
	defer writer.cacheMu.Unlock()

	switch op {
	case models.OpCreate, models.OpUpdate:
		writer.deviceCache[device.ID] = device
		slog.Debug("Device cache updated", "component", "EntityService", "op", op, "device_id", device.ID)
	case models.OpDelete:
		delete(writer.deviceCache, device.ID)
		slog.Debug("Device removed from cache", "component", "EntityService", "device_id", device.ID)
	}
}

// updateCredentialCache updates the in-memory credential cache based on CRUD operation
func (writer *EntityService) updateCredentialCache(op string, data interface{}) {
	cred, ok := data.(*models.CredentialProfile)
	if !ok {
		return
	}

	writer.cacheMu.Lock()
	defer writer.cacheMu.Unlock()

	switch op {
	case models.OpCreate, models.OpUpdate:
		writer.credentialCache[cred.ID] = cred
		slog.Debug("Credential cache updated", "component", "EntityService", "op", op, "cred_id", cred.ID)
	case models.OpDelete:
		delete(writer.credentialCache, cred.ID)
		slog.Debug("Credential removed from cache", "component", "EntityService", "cred_id", cred.ID)
	}
}

// LoadCaches loads all devices and credentials from DB into memory.
// Should be called once at startup before Run().
func (writer *EntityService) LoadCaches(ctx context.Context) error {
	writer.cacheMu.Lock()
	defer writer.cacheMu.Unlock()

	// Load credentials
	creds, err := writer.credentialRepo.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}
	for _, cred := range creds {
		writer.credentialCache[cred.ID] = cred
	}
	slog.Info("Loaded credentials to cache", "component", "EntityService", "count", len(creds))

	// Load devices (only active ones for scheduler)
	devices, err := writer.deviceRepo.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to load devices: %w", err)
	}
	for _, dev := range devices {
		writer.deviceCache[dev.ID] = dev
	}
	slog.Info("Loaded devices to cache", "component", "EntityService", "count", len(devices))

	return nil
}

// GetActiveDeviceIDs returns IDs of all active devices in cache.
// Used by Scheduler to initialize its priority queue.
func (writer *EntityService) GetActiveDeviceIDs() []int64 {
	writer.cacheMu.RLock()
	defer writer.cacheMu.RUnlock()

	ids := make([]int64, 0, len(writer.deviceCache))
	for id, dev := range writer.deviceCache {
		if dev.Status == "active" {
			ids = append(ids, id)
		}
	}
	return ids
}

// handleGetBatch handles batch device lookup by IDs.
// Returns devices split by should_ping flag.
func (writer *EntityService) handleGetBatch(req models.Request) models.Response {
	writer.cacheMu.RLock()
	defer writer.cacheMu.RUnlock()

	toPing := make([]*models.Device, 0)
	toSkip := make([]*models.Device, 0)
	seen := make(map[int64]struct{}, len(req.IDs))

	for _, id := range req.IDs {
		// The scheduler's priority queue may hold duplicate entries for a
		// device (lazy deletion: create/update events re-add the ID without
		// removing old entries). De-duplicate so a device is never polled
		// more than once per scheduler tick.
		if _, dup := seen[id]; dup {
			slog.Debug("Duplicate device ID in batch, skipping", "component", "EntityService", "device_id", id)
			continue
		}
		seen[id] = struct{}{}

		dev, exists := writer.deviceCache[id]
		if !exists {
			// Lazy queue management: device was deleted, skip silently
			slog.Debug("Device not found in cache (deleted?)", "component", "EntityService", "device_id", id)
			continue
		}
		// Only return active devices
		if dev.Status != "active" {
			continue
		}
		if dev.ShouldPing {
			toPing = append(toPing, dev)
		} else {
			toSkip = append(toSkip, dev)
		}
	}

	return models.Response{
		Data: &models.BatchDeviceResponse{
			ToPing: toPing,
			ToSkip: toSkip,
		},
	}
}

// handleGetCredential handles single credential lookup by profile ID.
func (writer *EntityService) handleGetCredential(req models.Request) models.Response {
	writer.cacheMu.RLock()
	defer writer.cacheMu.RUnlock()

	cred, exists := writer.credentialCache[req.ID]
	if !exists {
		return models.Response{
			Error: fmt.Errorf("credential profile %d not found", req.ID),
		}
	}
	return models.Response{Data: cred}
}

// handleDeactivateDevice deactivates a device by setting its status to inactive.
// Called by HealthMonitor when failure threshold is exceeded.
func (writer *EntityService) handleDeactivateDevice(ctx context.Context, deviceID int64) models.Response {
	device, err := writer.deviceRepo.Get(ctx, deviceID)
	if err != nil {
		return models.Response{Error: fmt.Errorf("device %d not found: %w", deviceID, err)}
	}

	// Update device status to inactive
	device.Status = "inactive"
	updatedDevice, err := writer.deviceRepo.Update(ctx, deviceID, device)
	if err != nil {
		return models.Response{Error: fmt.Errorf("failed to deactivate device %d: %w", deviceID, err)}
	}

	// Update cache with deactivated device
	writer.updateDeviceCache(models.OpUpdate, updatedDevice)

	// Publish event for cache invalidation in Scheduler
	sendEvent(writer.deviceEvents, models.Event{
		Type:    models.EventUpdate,
		Payload: updatedDevice,
	})

	slog.Info("Device deactivated", "component", "EntityService", "device_id", deviceID)
	return models.Response{Data: updatedDevice}
}

// cacheBackfill refreshes the in-memory caches periodically so devices deleted
// while the service runs are eventually removed (caches were previously
// reconciled only at startup).
func (writer *EntityService) cacheBackfill(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := writer.LoadCaches(ctx); err != nil {
				slog.Error("Cache backfill failed", "component", "EntityService", "error", err)
			}
		}
	}
}
