package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os/exec"
	"sync"
)

// Pool is a generic worker pool that executes plugin binaries with batched tasks
type Pool[T any, R any] struct {
	workerCount int
	poolName    string // For logging

	jobChan    chan Job[T]
	resultChan chan []R
}

// Job represents a batch of tasks for a single plugin
type Job[T any] struct {
	BinPath string // Absolute path to plugin binary
	Tasks   []T
}

// NewPool creates a new generic worker pool
func NewPool[T any, R any](workerCount int, poolName string) *Pool[T, R] {
	return &Pool[T, R]{
		workerCount: workerCount,
		poolName:    poolName,
		jobChan:     make(chan Job[T], 100),
		resultChan:  make(chan []R, 100),
	}
}

// Start begins the worker pool (call once at startup)
func (p *Pool[T, R]) Start(ctx context.Context) {
	log.Printf("[%s] Starting %d workers", p.poolName, p.workerCount)

	var wg sync.WaitGroup
	for i := 0; i < p.workerCount; i++ {
		wg.Add(1)
		go p.worker(ctx, i, &wg)
	}

	// Wait for all workers to finish when context is done
	go func() {
		wg.Wait()
		close(p.resultChan)
		log.Printf("[%s] All workers stopped", p.poolName)
	}()
}

// Submit sends a batch of tasks to the pool with the plugin binary path
func (p *Pool[T, R]) Submit(binPath string, tasks []T) {
	p.jobChan <- Job[T]{
		BinPath: binPath,
		Tasks:   tasks,
	}
}

// Results returns the channel for receiving results
func (p *Pool[T, R]) Results() <-chan []R {
	return p.resultChan
}

// worker processes jobs continuously
func (p *Pool[T, R]) worker(ctx context.Context, id int, wg *sync.WaitGroup) {
	defer wg.Done()
	log.Printf("[%s] Worker %d started", p.poolName, id)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] Worker %d stopping", p.poolName, id)
			return

		case job, ok := <-p.jobChan:
			if !ok {
				log.Printf("[%s] Worker %d: job channel closed", p.poolName, id)
				return
			}

			results := p.executePlugin(job)
			p.resultChan <- results
		}
	}
}

// executePlugin runs the plugin binary with the batch of tasks
func (p *Pool[T, R]) executePlugin(job Job[T]) []R {
	log.Printf("[%s] Executing %s with %d tasks", p.poolName, job.BinPath, len(job.Tasks))

	// Marshal tasks to JSON
	inputJSON, err := json.Marshal(job.Tasks)
	if err != nil {
		log.Printf("[%s] Failed to marshal tasks: %v", p.poolName, err)
		return []R{} // Return empty on error
	}

	// Execute plugin
	cmd := exec.Command(job.BinPath)
	cmd.Stdin = bytes.NewReader(inputJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		log.Printf("[%s] Plugin %s failed: %v, stderr: %s", p.poolName, job.BinPath, err, stderr.String())
		return []R{} // Return empty on error
	}

	// Parse results
	var results []R
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		log.Printf("[%s] Failed to parse results: %v", p.poolName, err)
		return []R{} // Return empty on error
	}

	log.Printf("[%s] Plugin %s returned %d results", p.poolName, job.BinPath, len(results))
	return results
}
