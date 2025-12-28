package pluginWorker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os/exec"
	"sync"
)

// PluginWorkerPool is a generic pluginWorker pool that executes plugin binaries with batched tasks
type PluginWorkerPool[T any, R any] struct {
	workerCount int
	poolName    string   // For logging
	args        []string // Continuous arguments for every execution

	jobChan    chan Job[T]
	resultChan chan []R
}

// Job represents a batch of tasks for a single plugin
type Job[T any] struct {
	BinPath string // Absolute path to plugin binary
	Tasks   []T
}

// NewPool creates a new generic pluginWorker pool
func NewPool[T any, R any](workerCount int, poolName string, bufferSize int, args ...string) *PluginWorkerPool[T, R] {
	return &PluginWorkerPool[T, R]{
		workerCount: workerCount,
		poolName:    poolName,
		args:        args,
		jobChan:     make(chan Job[T], bufferSize),
		resultChan:  make(chan []R, bufferSize),
	}
}

// Start begins the pluginWorker pool (call once at startup)
func (pool *PluginWorkerPool[T, R]) Start(ctx context.Context) {
	slog.Info("Starting pluginWorker pool", "component", pool.poolName, "worker_count", pool.workerCount)

	var wg sync.WaitGroup
	for i := 0; i < pool.workerCount; i++ {
		wg.Add(1)
		go pool.worker(ctx, i, &wg)
	}

	// Wait for all workers to finish when context is done
	go func() {
		wg.Wait()
		close(pool.resultChan)
		slog.Info("All workers stopped", "component", pool.poolName)
	}()
}

// Submit sends a batch of tasks to the pool with the plugin binary path
func (pool *PluginWorkerPool[T, R]) Submit(binPath string, tasks []T) {
	pool.jobChan <- Job[T]{
		BinPath: binPath,
		Tasks:   tasks,
	}
}

// Results returns the channel for receiving results
func (pool *PluginWorkerPool[T, R]) Results() <-chan []R {
	return pool.resultChan
}

// worker processes jobs continuously
func (pool *PluginWorkerPool[T, R]) worker(ctx context.Context, id int, wg *sync.WaitGroup) {
	defer wg.Done()
	slog.Info("Worker started", "component", pool.poolName, "worker_id", id)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Worker stopping", "component", pool.poolName, "worker_id", id)
			return

		case job, ok := <-pool.jobChan:
			if !ok {
				slog.Info("Worker job channel closed", "component", pool.poolName, "worker_id", id)
				return
			}

			results := pool.executePlugin(job)
			pool.resultChan <- results
		}
	}
}

// todo  rename pluginWorker to meaningful name

// executePlugin runs the plugin binary with the batch of tasks
func (pool *PluginWorkerPool[T, R]) executePlugin(job Job[T]) []R {
	slog.Debug("Executing plugin", "component", pool.poolName, "bin_path", job.BinPath, "task_count", len(job.Tasks))

	// Marshal tasks to JSON
	inputJSON, err := json.Marshal(job.Tasks)
	if err != nil {
		slog.Error("Failed to marshal tasks", "component", pool.poolName, "error", err)
		return []R{} // Return empty on error
	}

	// Execute plugin
	cmd := exec.Command(job.BinPath, pool.args...)
	cmd.Stdin = bytes.NewReader(inputJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		slog.Error("Plugin failed", "component", pool.poolName, "bin_path", job.BinPath, "error", err, "stderr", stderr.String())
		return []R{} // Return empty on error
	}

	// Parse results
	var results []R
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		slog.Error("Failed to parse results", "component", pool.poolName, "error", err)
		return []R{} // Return empty on error
	}

	slog.Debug("Plugin returned results", "component", pool.poolName, "bin_path", job.BinPath, "result_count", len(results))
	return results
}
