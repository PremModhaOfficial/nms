package scheduling

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"nms/pkg/models"
)

// Scheduler manages the scheduling of devices based on deadlines.
// Uses a min-heap priority queue to efficiently find expired deadlines.
type Scheduler struct {
	// Priority queue ordered by deadline (min-heap)
	queue DeadlineQueue

	// Request channel to EntityService for device lookups
	entityReqChan chan<- models.Request

	// Channels - received from outside for event-driven communication
	deviceEvents <-chan models.Event     // Device create/update events (to add to queue)
	OutputChan   chan<- []*models.Device // Sends qualified devices to poller

	// Config
	fpingPath    string
	tickInterval time.Duration
	fpingTimeout int
	fpingRetries int
}

// NewScheduler creates a new Scheduler instance.
func NewScheduler(
	deviceEvents <-chan models.Event,
	entityReqChan chan<- models.Request,
	outputChan chan<- []*models.Device,
	fpingPath string,
	tickIntervalSec, fpingTimeoutMs, fpingRetries int,
) *Scheduler {
	return &Scheduler{
		queue:         make(DeadlineQueue, 0),
		entityReqChan: entityReqChan,
		deviceEvents:  deviceEvents,
		OutputChan:    outputChan,
		fpingPath:     fpingPath,
		tickInterval:  time.Duration(tickIntervalSec) * time.Second,
		fpingTimeout:  fpingTimeoutMs,
		fpingRetries:  fpingRetries,
	}
}

// InitQueue initializes the priority queue with device IDs.
// All devices start with deadline = now (immediately eligible).
func (sched *Scheduler) InitQueue(deviceIDs []int64) {
	now := time.Now()
	sched.queue.InitQueue(deviceIDs, now)
	slog.Info("Priority queue initialized", "component", "Scheduler", "device_count", len(deviceIDs))
}

// Run starts the main loop.
func (sched *Scheduler) Run(ctx context.Context) {
	slog.Info("Starting main loop", "component", "Scheduler", "tick_interval", sched.tickInterval.String())
	ticker := time.NewTicker(sched.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Context cancelled, shutting down", "component", "Scheduler")
			return

		case event := <-sched.deviceEvents:
			slog.Debug("Received device event", "component", "Scheduler", "event_type", event.Type)
			sched.processDeviceEvent(event)

		case <-ticker.C:
			slog.Debug("Tick - running schedule()", "component", "Scheduler")
			sched.schedule()
		}
	}
}

// processDeviceEvent handles create/update events to add devices to queue.
// Note: Delete events are handled lazily - EntityService won't return deleted devices.
func (sched *Scheduler) processDeviceEvent(event models.Event) {
	payload, ok := event.Payload.(*models.Device)
	if !ok {
		slog.Error("Invalid payload type in device event", "component", "Scheduler")
		return
	}

	switch event.Type {
	case models.EventCreate:
		// New device: add to queue with immediate deadline
		sched.queue.PushEntry(payload.ID, time.Now())
		slog.Info("Added new device to queue", "component", "Scheduler", "device_id", payload.ID)
	case models.EventUpdate:
		// Updated device: add a new entry with immediate deadline
		// The old entry may still exist (lazy management) but will be handled gracefully
		sched.queue.PushEntry(payload.ID, time.Now())
		slog.Info("Re-added updated device to queue", "component", "Scheduler", "device_id", payload.ID)
	case models.EventDelete:
		// Lazy deletion: don't remove from queue, EntityService won't return it
		slog.Debug("Device delete event received (lazy queue management)", "component", "Scheduler", "device_id", payload.ID)
	}
}

// schedule pops expired entries, fetches device details from EntityService,
// performs fping, and dispatches qualified devices to Poller.
func (sched *Scheduler) schedule() {
	now := time.Now()
	slog.Debug("Checking deadlines", "component", "Scheduler", "now", now.Format(time.RFC3339), "queue_size", sched.queue.Len())

	// 1. Pop all expired entries from queue
	expired := sched.queue.PopExpired(now)
	if len(expired) == 0 {
		slog.Debug("No candidates due for polling", "component", "Scheduler")
		return
	}

	// Collect device IDs
	deviceIDs := make([]int64, 0, len(expired))
	deadlineMap := make(map[int64]time.Time) // Track original deadlines for re-insertion
	for _, entry := range expired {
		deviceIDs = append(deviceIDs, entry.DeviceID)
		deadlineMap[entry.DeviceID] = entry.Deadline
	}

	slog.Debug("Expired entries popped", "component", "Scheduler", "count", len(deviceIDs))

	// 2. Request device details from EntityService
	replyCh := make(chan models.Response, 1)
	sched.entityReqChan <- models.Request{
		Operation: models.OpGetBatch,
		IDs:       deviceIDs,
		ReplyCh:   replyCh,
	}

	resp := <-replyCh
	if resp.Error != nil {
		slog.Error("Failed to get devices from EntityService", "component", "Scheduler", "error", resp.Error)
		// Re-add entries back to queue to retry later
		for _, entry := range expired {
			sched.queue.PushEntry(entry.DeviceID, entry.Deadline.Add(sched.tickInterval))
		}
		return
	}

	batchResp, ok := resp.Data.(*models.BatchDeviceResponse)
	if !ok {
		slog.Error("Invalid response type from EntityService", "component", "Scheduler")
		return
	}

	slog.Debug("Got devices from EntityService", "component", "Scheduler", "to_ping", len(batchResp.ToPing), "to_skip", len(batchResp.ToSkip))

	// 3. Collect IPs for fping (only from ToPing list)
	ips := make([]string, 0)
	ipSet := make(map[string]bool)
	for _, dev := range batchResp.ToPing {
		if !ipSet[dev.IPAddress] {
			ips = append(ips, dev.IPAddress)
			ipSet[dev.IPAddress] = true
		}
	}

	// 4. Perform batch fping
	reachableIPs := sched.performBatchFping(ips)
	slog.Debug("Fping results", "component", "Scheduler", "reachable_count", len(reachableIPs), "total_ips", len(ips))

	// 5. Filter qualified devices and collect entries for re-add
	qualified := make([]*models.Device, 0)
	toRequeue := make([]*DeviceDeadline, 0, len(batchResp.ToPing)+len(batchResp.ToSkip))

	// Process ToPing devices
	for _, dev := range batchResp.ToPing {
		oldDeadline := deadlineMap[dev.ID]
		newDeadline := oldDeadline.Add(time.Duration(dev.PollingIntervalSeconds) * time.Second)

		if reachableIPs[dev.IPAddress] {
			qualified = append(qualified, dev)
			slog.Info("Device qualified (ping OK)", "component", "Scheduler", "device_id", dev.ID, "next_deadline", newDeadline.Format(time.RFC3339))
		} else {
			slog.Debug("Device not reachable", "component", "Scheduler", "device_id", dev.ID, "ip", dev.IPAddress)
		}

		// Collect for batch re-add
		toRequeue = append(toRequeue, &DeviceDeadline{DeviceID: dev.ID, Deadline: newDeadline})
	}

	// Process ToSkip devices (no ping needed, always qualified)
	for _, dev := range batchResp.ToSkip {
		oldDeadline := deadlineMap[dev.ID]
		newDeadline := oldDeadline.Add(time.Duration(dev.PollingIntervalSeconds) * time.Second)

		qualified = append(qualified, dev)
		slog.Info("Device qualified (ping skipped)", "component", "Scheduler", "device_id", dev.ID, "next_deadline", newDeadline.Format(time.RFC3339))

		// Collect for batch re-add
		toRequeue = append(toRequeue, &DeviceDeadline{DeviceID: dev.ID, Deadline: newDeadline})
	}

	// 6. Batch re-add to queue (O(n) instead of O(k log n))
	if len(toRequeue) > 0 {
		sched.queue.PushBatch(toRequeue)
	}

	// 7. Dispatch qualified list to OutputChan
	if len(qualified) > 0 {
		slog.Info("Dispatching qualified devices", "component", "Scheduler", "count", len(qualified))
		sched.OutputChan <- qualified
	} else {
		slog.Debug("No devices qualified", "component", "Scheduler")
	}
}

// performBatchFping runs fping against a list of IPs and returns reachability.
func (sched *Scheduler) performBatchFping(ips []string) map[string]bool {
	reachable := make(map[string]bool)

	if len(ips) == 0 {
		slog.Debug("No IPs to check with fping", "component", "Scheduler")
		return reachable
	}

	slog.Info("Checking IPs with fping", "component", "Scheduler", "count", len(ips), "timeout_ms", sched.fpingTimeout, "retries", sched.fpingRetries)

	// Build fping command
	// -a: show alive hosts
	// -q: quiet (don't show per-target results)
	// -t: timeout in ms
	// -r: retry count
	args := []string{
		"-a",
		"-q",
		"-t", fmt.Sprintf("%d", sched.fpingTimeout),
		"-r", fmt.Sprintf("%d", sched.fpingRetries),
	}
	args = append(args, ips...)

	cmd := exec.Command(sched.fpingPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	// fping returns non-zero if some hosts are unreachable, so we don't treat that as an error
	if err != nil {
		slog.Debug("fping exited with error (normal if some hosts down)", "component", "Scheduler", "error", err)
	}

	// Parse stdout for reachable IPs (one per line)
	output := strings.TrimSpace(stdout.String())
	if output != "" {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			ip := strings.TrimSpace(line)
			if ip != "" {
				reachable[ip] = true
				slog.Debug("IP is reachable", "component", "Scheduler", "ip", ip)
			}
		}
	}

	slog.Info("Fping check complete", "component", "Scheduler", "reachable_count", len(reachable), "total_ips", len(ips))
	return reachable
}
