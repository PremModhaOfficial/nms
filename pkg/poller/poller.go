package poller

import (
	"context"
	"log/slog"
	"nms/pkg/api"
	"os"
	"path/filepath"

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

	// Input channel: receives batches of devices from scheduler
	InputChan <-chan []*models.Device

	// Output channel: sends aggregated poll results
	OutputChan chan<- []plugin.Result
}

// NewPoller creates a new Poller instance.
func NewPoller(pluginDir string, encryptionKey string, workerCount int, bufferSize int, inputChan <-chan []*models.Device, outputChan chan<- []plugin.Result) *Poller {
	pool := worker.NewPool[plugin.Task, plugin.Result](workerCount, "PollPool", bufferSize)

	p := &Poller{
		pool:          pool,
		pluginDir:     pluginDir,
		plugins:       make(map[string]string),
		encryptionKey: encryptionKey,
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

// createTasks converts devices to plugin.Task
func (poller *Poller) createTasks(devices []*models.Device) []plugin.Task {
	tasks := make([]plugin.Task, 0, len(devices))
	for _, d := range devices {
		// Decrypt credentials payload
		payload, err := api.DecryptPayload(d.CredentialProfile, poller.encryptionKey)
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
