package poller

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"nms/pkg/api"
	"nms/pkg/models"
	"nms/pkg/plugin"
	"nms/pkg/worker"
)

// Poller manages plugin execution for polling devices.
type Poller struct {
	pool          *worker.Pool[plugin.Task, plugin.Result]
	pluginDir     string
	plugins       map[string]string // pluginID -> binary path
	encryptionKey string

	// Request channel to EntityService for credential lookups
	entityReqChan chan<- models.Request

	// Input channel: receives batches of devices from scheduler
	InputChan <-chan []*models.Device

	// Output channel: sends aggregated poll results
	OutputChan chan<- []plugin.Result
}

// NewPoller creates a new Poller instance.
func NewPoller(
	pluginDir string,
	encryptionKey string,
	workerCount int,
	bufferSize int,
	entityReqChan chan<- models.Request,
	inputChan <-chan []*models.Device,
	outputChan chan<- []plugin.Result,
) *Poller {
	pool := worker.NewPool[plugin.Task, plugin.Result](workerCount, "PollPool", bufferSize)

	p := &Poller{
		pool:          pool,
		pluginDir:     pluginDir,
		plugins:       make(map[string]string),
		encryptionKey: encryptionKey,
		entityReqChan: entityReqChan,
		InputChan:     inputChan,
		OutputChan:    outputChan,
	}
	p.loadPlugins()
	return p
}

// loadPlugins scans the plugin directory and populates the plugins map.
// Each subdirectory is a plugin; the binary must be named the same as the directory.
func (poller *Poller) loadPlugins() {
	slog.Info("Scanning plugins", "component", "Poller", "dir", poller.pluginDir)

	entries, err := os.ReadDir(poller.pluginDir)
	if err != nil {
		slog.Error("Failed to scan plugin directory", "component", "Poller", "error", err)
		return
	}

	for _, entry := range entries {
		pluginID := entry.Name()
		var binPath string

		if entry.IsDir() {
			// Option 1: pluginDir/ID/ID
			binPath = filepath.Join(poller.pluginDir, pluginID, pluginID)
			if _, err := os.Stat(binPath); err != nil {
				continue
			}
		} else {
			// Option 2: pluginDir/ID
			binPath = filepath.Join(poller.pluginDir, pluginID)
		}

		poller.plugins[pluginID] = binPath
		slog.Info("Loaded plugin", "component", "Poller", "plugin_id", pluginID, "path", binPath)
	}
	slog.Info("Plugins loaded", "component", "Poller", "count", len(poller.plugins))
}

// Run starts the poller's main loop.
func (poller *Poller) Run(ctx context.Context) {
	slog.Info("Starting main loop", "component", "Poller")

	// Start the worker pool
	poller.pool.Start(ctx)

	// Start result collector
	go poller.collectResults(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Context cancelled, shutting down", "component", "Poller")
			return

		case devices := <-poller.InputChan:
			slog.Info("Received devices from scheduler", "component", "Poller", "count", len(devices))

			// Group devices by PluginID
			grouped := poller.groupByProtocol(devices)

			// Submit jobs to pool with binary paths
			for pluginID, deviceList := range grouped {
				binPath, exists := poller.plugins[pluginID]
				if !exists {
					slog.Error("Plugin not found", "component", "Poller", "plugin_id", pluginID, "device_count", len(deviceList))
					continue
				}

				tasks := poller.createTasks(deviceList)
				poller.pool.Submit(binPath, tasks)
			}
		}
	}
}

// groupByProtocol groups devices by their PluginID.
func (poller *Poller) groupByProtocol(devices []*models.Device) map[string][]*models.Device {
	grouped := make(map[string][]*models.Device)
	for _, d := range devices {
		grouped[d.PluginID] = append(grouped[d.PluginID], d)
	}
	return grouped
}

// getCredential fetches a credential from EntityService cache.
func (poller *Poller) getCredential(profileID int64) *models.CredentialProfile {
	replyCh := make(chan models.Response, 1)
	poller.entityReqChan <- models.Request{
		Operation: models.OpGetCredential,
		ID:        profileID,
		ReplyCh:   replyCh,
	}

	resp := <-replyCh
	if resp.Error != nil {
		slog.Error("Failed to get credential", "component", "Poller", "profile_id", profileID, "error", resp.Error)
		return nil
	}

	cred, ok := resp.Data.(*models.CredentialProfile)
	if !ok {
		slog.Error("Invalid credential response type", "component", "Poller", "profile_id", profileID)
		return nil
	}
	return cred
}

// createTasks converts devices to plugin.Task, fetching credentials from EntityService.
func (poller *Poller) createTasks(devices []*models.Device) []plugin.Task {
	tasks := make([]plugin.Task, 0, len(devices))

	// Cache credentials by profile ID to avoid duplicate requests
	credCache := make(map[int64]*models.CredentialProfile)

	for _, d := range devices {
		// Get credential from cache or fetch from EntityService
		cred, exists := credCache[d.CredentialProfileID]
		if !exists {
			cred = poller.getCredential(d.CredentialProfileID)
			credCache[d.CredentialProfileID] = cred
		}

		// Decrypt credentials payload
		payload, err := api.DecryptPayload(cred, poller.encryptionKey)
		if err != nil {
			slog.Error("Failed to decrypt credentials", "component", "Poller", "device_id", d.ID, "error", err)
			payload = nil // Plugin will handle missing credentials
		}

		task := plugin.Task{
			DeviceID:    d.ID,
			Target:      d.IPAddress,
			Port:        d.Port,
			Credentials: payload,
		}
		tasks = append(tasks, task)
	}
	return tasks
}

// collectResults aggregates results from pool and sends to OutputChan
func (poller *Poller) collectResults(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case results, ok := <-poller.pool.Results():
			if !ok {
				return
			}
			if len(results) > 0 {
				poller.OutputChan <- results
			}
		}
	}
}
