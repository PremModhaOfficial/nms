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

// Poller manages plugin execution for polling monitors.
type Poller struct {
	pool          *worker.Pool[plugin.Task, plugin.Result]
	pluginDir     string
	plugins       map[string]string // pluginID -> binary path
	encryptionKey string

	// Input channel: receives batches of monitors from scheduler
	InputChan <-chan []*models.Monitor

	// Output channel: sends aggregated poll results
	OutputChan chan<- []plugin.Result
}

// NewPoller creates a new Poller instance.
func NewPoller(pluginDir string, encryptionKey string, workerCount int, bufferSize int, inputChan <-chan []*models.Monitor, outputChan chan<- []plugin.Result) *Poller {
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

		case monitors := <-poller.InputChan:
			slog.Info("Received monitors from scheduler", "component", "Poller", "count", len(monitors))

			// Group monitors by PluginID
			grouped := poller.groupByProtocol(monitors)

			// Submit jobs to pool with binary paths
			for pluginID, monitorList := range grouped {
				binPath, exists := poller.plugins[pluginID]
				if !exists {
					slog.Warn("Plugin not found", "component", "Poller", "plugin_id", pluginID, "monitor_count", len(monitorList))
					continue
				}

				tasks := poller.createTasks(monitorList)
				poller.pool.Submit(binPath, tasks)
			}
		}
	}
}

// groupByProtocol groups monitors by their PluginID.
func (poller *Poller) groupByProtocol(monitors []*models.Monitor) map[string][]*models.Monitor {
	grouped := make(map[string][]*models.Monitor)
	for _, m := range monitors {
		grouped[m.PluginID] = append(grouped[m.PluginID], m)
	}
	return grouped
}

// createTasks converts monitors to plugin.Task
func (poller *Poller) createTasks(monitors []*models.Monitor) []plugin.Task {
	tasks := make([]plugin.Task, 0, len(monitors))
	for _, m := range monitors {
		// Decrypt credentials payload
		payload, err := api.DecryptPayload(m.CredentialProfile, poller.encryptionKey)
		if err != nil {
			slog.Error("Failed to decrypt credentials", "component", "Poller", "monitor_id", m.ID, "error", err)
			payload = nil // Plugin will handle missing credentials
		}

		task := plugin.Task{
			MonitorID:   m.ID,
			Target:      m.IPAddress,
			Port:        m.Port,
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
