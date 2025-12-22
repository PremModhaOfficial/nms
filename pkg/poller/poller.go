package poller

import (
	"context"
	"log"
	"nms/pkg/models"
	"nms/pkg/plugin"
	"nms/pkg/worker"
	"path/filepath"
)

// Poller manages plugin execution for polling monitors.
type Poller struct {
	pool      *worker.Pool[plugin.Task, plugin.Result]
	pluginDir string
	plugins   map[string]string // pluginID -> binary path

	// Input channel: receives batches of monitors from scheduler
	InputChan <-chan []*models.Monitor

	// Output channel: sends aggregated poll results
	OutputChan chan<- []plugin.Result
}

// NewPoller creates a new Poller instance.
func NewPoller(pluginDir string, workerCount int, inputChan <-chan []*models.Monitor, outputChan chan<- []plugin.Result) *Poller {
	pool := worker.NewPool[plugin.Task, plugin.Result](workerCount, "PollPool")

	p := &Poller{
		pool:       pool,
		pluginDir:  pluginDir,
		plugins:    make(map[string]string),
		InputChan:  inputChan,
		OutputChan: outputChan,
	}
	p.loadPlugins()
	return p
}

// loadPlugins scans the plugin directory and populates the plugins map
func (p *Poller) loadPlugins() {
	log.Printf("[Poller] Loading plugins from: %s", p.pluginDir)
	// TODO: Scan pluginDir and populate p.plugins map dynamically
	// For now, manually add known plugins
	p.plugins["winrm"] = filepath.Join(p.pluginDir, "winrm", "winrm")
	log.Printf("[Poller] Loaded %d plugins", len(p.plugins))
}

// Run starts the poller's main loop.
func (p *Poller) Run(ctx context.Context) {
	log.Println("[Poller] Run: Starting main loop")

	// Start the worker pool
	p.pool.Start(ctx)

	// Start result collector
	go p.collectResults(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("[Poller] Run: Context cancelled, shutting down")
			return

		case monitors := <-p.InputChan:
			log.Printf("[Poller] Run: Received %d monitors from scheduler", len(monitors))

			// Group monitors by PluginID
			grouped := p.groupByProtocol(monitors)

			// Submit jobs to pool with binary paths
			for pluginID, monitorList := range grouped {
				binPath, exists := p.plugins[pluginID]
				if !exists {
					log.Printf("[Poller] Plugin not found: %s, skipping %d monitors", pluginID, len(monitorList))
					continue
				}

				tasks := p.createTasks(monitorList)
				p.pool.Submit(binPath, tasks)
			}
		}
	}
}

// groupByProtocol groups monitors by their PluginID.
func (p *Poller) groupByProtocol(monitors []*models.Monitor) map[string][]*models.Monitor {
	grouped := make(map[string][]*models.Monitor)
	for _, m := range monitors {
		grouped[m.PluginID] = append(grouped[m.PluginID], m)
	}
	return grouped
}

// createTasks converts monitors to plugin.Task
func (p *Poller) createTasks(monitors []*models.Monitor) []plugin.Task {
	tasks := make([]plugin.Task, 0, len(monitors))
	for _, m := range monitors {
		// Decrypt credentials payload
		payload, err := plugin.DecryptPayload(m.CredentialProfile)
		if err != nil {
			log.Printf("[Poller] Failed to decrypt credentials for monitor %d: %v", m.ID, err)
			payload = "" // Plugin will handle missing credentials
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
func (p *Poller) collectResults(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case results, ok := <-p.pool.Results():
			if !ok {
				return
			}
			if len(results) > 0 {
				p.OutputChan <- results
			}
		}
	}
}
