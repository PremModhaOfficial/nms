package pluginWorker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// pluginExecTimeout bounds how long a single plugin invocation may run.
// A hung plugin binary must never occupy a worker (and therefore starve the
// whole poll/discovery pipeline) forever.
const pluginExecTimeout = 5 * time.Minute

// PluginWorkerPool is a generic pluginWorker pool that executes plugin binaries with batched tasks
type PluginWorkerPool[T any, R any] struct {
	workerCount int
	poolName    string   // For logging
	args        []string // Continuous arguments for every execution

	jobChan    chan Job[T]
	resultChan chan []R

	// ctx is the lifecycle context supplied by Start. It guards Submit and
	// result sends so a cancelled context never leaves callers or workers
	// blocked forever (shutdown deadlock).
	ctx     context.Context
	startMu sync.Mutex
	started bool
}

// Job represents a batch of tasks for a single plugin
type Job[T any] struct {
	BinPath string // Absolute path to plugin binary
	Tasks   []T
}

// NewPool creates a new generic pluginWorker pool
func NewPool[T any, R any](workerCount int, poolName string, bufferSize int, args ...string) *PluginWorkerPool[T, R] {
	if workerCount <= 0 {
		panic(fmt.Sprintf("pluginWorker.NewPool: workerCount must be > 0, got %d", workerCount))
	}
	if bufferSize < 0 {
		panic(fmt.Sprintf("pluginWorker.NewPool: bufferSize must be >= 0, got %d", bufferSize))
	}
	return &PluginWorkerPool[T, R]{
		workerCount: workerCount,
		poolName:    poolName,
		args:        args,
		jobChan:     make(chan Job[T], bufferSize),
		resultChan:  make(chan []R, bufferSize),
		ctx:         context.Background(),
	}
}

// Start begins the pluginWorker pool (call once at startup)
func (pool *PluginWorkerPool[T, R]) Start(ctx context.Context) {
	pool.startMu.Lock()
	if pool.started {
		pool.startMu.Unlock()
		panic(fmt.Sprintf("pluginWorker.Start: pool %q already started", pool.poolName))
	}
	pool.started = true
	pool.ctx = ctx
	pool.startMu.Unlock()

	slog.Info("Starting pluginWorker pool", "component", pool.poolName, "worker_count", pool.workerCount)

	var wg sync.WaitGroup
	for i := 0; i < pool.workerCount; i++ {
		wg.Add(1)
		go pool.worker(i, &wg)
	}

	// Wait for all workers to finish when context is done
	go func() {
		wg.Wait()
		close(pool.resultChan)
		slog.Info("All workers stopped", "component", pool.poolName)
	}()
}

// Submit sends a batch of tasks to the pool with the plugin binary path.
// It returns false if the pool is shut down (ctx cancelled), so producers
// (e.g. discovery/poll loops) can never wedge on a stopped pool.
func (pool *PluginWorkerPool[T, R]) Submit(binPath string, tasks []T) bool {
	// Pre-check: once cancelled, never accept new work even if a worker is idle
	// (a raw select could otherwise randomly pick the ready send case).
	if pool.ctx.Err() != nil {
		return false
	}
	select {
	case <-pool.ctx.Done():
		return false
	case pool.jobChan <- Job[T]{
		BinPath: binPath,
		Tasks:   tasks,
	}:
		return true
	}
}

// Results returns the channel for receiving results
func (pool *PluginWorkerPool[T, R]) Results() <-chan []R {
	return pool.resultChan
}

// worker processes jobs continuously
func (pool *PluginWorkerPool[T, R]) worker(id int, wg *sync.WaitGroup) {
	defer wg.Done()
	slog.Info("Worker started", "component", pool.poolName, "worker_id", id)

	for {
		select {
		case <-pool.ctx.Done():
			slog.Info("Worker stopping", "component", pool.poolName, "worker_id", id)
			return

		case job, ok := <-pool.jobChan:
			if !ok {
				slog.Info("Worker job channel closed", "component", pool.poolName, "worker_id", id)
				return
			}

			results := pool.executePlugin(job)
			// Guard the result send against shutdown: if the consumer has
			// stopped, drop the results instead of leaking the worker forever.
			select {
			case <-pool.ctx.Done():
				slog.Info("Worker dropping results during shutdown", "component", pool.poolName, "worker_id", id)
				return
			case pool.resultChan <- results:
			}
		}
	}
}

// executePlugin runs the plugin binary with the batch of tasks.
// A panic inside the plugin path (e.g. a malformed task) is contained so one
// bad job cannot kill the worker and, with it, the whole pipeline.
func (pool *PluginWorkerPool[T, R]) executePlugin(job Job[T]) (results []R) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic recovered in plugin execution", "component", pool.poolName, "bin_path", job.BinPath, "error", r)
			results = []R{}
		}
	}()

	slog.Debug("Executing plugin", "component", pool.poolName, "bin_path", job.BinPath, "task_count", len(job.Tasks))

	// Marshal tasks to JSON
	inputJSON, err := json.Marshal(job.Tasks)
	if err != nil {
		slog.Error("Failed to marshal tasks", "component", pool.poolName, "error", err)
		return []R{} // Return empty on error
	}

	// Execute plugin with an explicit deadline so a hung binary cannot
	// permanently occupy a worker, and cancel it on pool shutdown.
	execCtx, cancel := context.WithTimeout(pool.ctx, pluginExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, job.BinPath, pool.args...)
	// Put the plugin in its own process group and kill the whole group on
	// timeout/cancellation. Killing only the direct child would leave
	// grandchildren (e.g. `sh` spawning `sleep`) holding stdout, so
	// cmd.Run() would keep blocking past the bound.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.Stdin = bytes.NewReader(inputJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		slog.Error("Plugin failed", "component", pool.poolName, "bin_path", job.BinPath, "error", err, "stderr", stderr.String())
		return []R{} // Return empty on error
	}

	// Parse results
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		slog.Error("Failed to parse results", "component", pool.poolName, "error", err)
		return []R{} // Return empty on error
	}

	slog.Debug("Plugin returned results", "component", pool.poolName, "bin_path", job.BinPath, "result_count", len(results))
	return results
}
