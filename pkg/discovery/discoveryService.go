package discovery

import (
	"context"
	"log"
	"nms/pkg/database"
	"nms/pkg/models"
	"nms/pkg/plugin"
	"nms/pkg/worker"
)

// DiscoveryService coordinates the discovery process and provisioning.
// It listens for DiscoveryProfile events and manages the DiscoveryPool.
type DiscoveryService struct {
	pool     *worker.Pool[plugin.Task, plugin.Result]
	devRepo  database.Repository[models.Device]
	monRepo  database.Repository[models.Monitor]
	credRepo database.Repository[models.CredentialProfile]

	// Input channel for events (reacts like the scheduler)
	InputChan chan models.Event
}

// NewDiscoveryService creates a new discovery service.
func NewDiscoveryService(
	pool *worker.Pool[plugin.Task, plugin.Result],
	devRepo database.Repository[models.Device],
	monRepo database.Repository[models.Monitor],
	credRepo database.Repository[models.CredentialProfile],
) *DiscoveryService {
	return &DiscoveryService{
		pool:      pool,
		devRepo:   devRepo,
		monRepo:   monRepo,
		credRepo:  credRepo,
		InputChan: make(chan models.Event, 100),
	}
}

// Start initiates the discovery result listener and event processor.
func (s *DiscoveryService) Start(ctx context.Context) {
	log.Println("[DiscoveryService] Starting service")

	// Start result collector
	go s.collectResults(ctx)

	// Main event loop (similar to scheduler)
	for {
		select {
		case <-ctx.Done():
			log.Println("[DiscoveryService] Stopping service")
			return
		case event := <-s.InputChan:
			s.processEvent(event)
		}
	}
}

// processEvent handles CRUD events for DiscoveryProfiles.
func (s *DiscoveryService) processEvent(event models.Event) {
	// TODO: Handle DiscoveryProfile Create/Update/Delete
	log.Printf("[DiscoveryService] Received event: %s", event.Type)
}

// collectResults listens for results from the worker pool.
func (s *DiscoveryService) collectResults(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case results, ok := <-s.pool.Results():
			if !ok {
				return
			}
			for _, res := range results {
				if res.Success && res.Hostname != "" {
					s.provisionDevice(ctx, res)
				}
			}
		}
	}
}

// provisionDevice converts a successful discovery into a Device and Monitor.
func (s *DiscoveryService) provisionDevice(ctx context.Context, res plugin.Result) {
	// TODO: Check if device already exists.
	// TODO: Create Device and Monitor records.
	log.Printf("[DiscoveryService] Provisioning device: %s (%s)", res.Hostname, res.Target)
}

// RunProfile triggers discovery for a specific profile.
func (s *DiscoveryService) RunProfile(ctx context.Context, profile *models.DiscoveryProfile, binPath string) {
	// TODO: Expand Target to IPs and submit to pool.
	log.Printf("[DiscoveryService] Running discovery for profile: %s", profile.Name)
}
