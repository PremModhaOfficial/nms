package scheduler

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

// MonitorWithDeadline combines a monitor with its next scheduled polling time.
type MonitorWithDeadline struct {
	Monitor  *models.Monitor
	Deadline time.Time
}

// Scheduler manages the scheduling of monitors based on deadlines.
type Scheduler struct {
	// Cache maps
	monitors map[int64]*MonitorWithDeadline
	creds    map[int64]*models.CredentialProfile

	// Channels - received from outside for event-driven communication
	monitorEvents <-chan models.Event      // Monitor CRUD events
	credEvents    <-chan models.Event      // Credential CRUD events
	OutputChan    chan<- []*models.Monitor // Sends qualified monitors to poller

	// Config
	fpingPath    string
	tickInterval time.Duration
	fpingTimeout int
	fpingRetries int
}

// NewScheduler creates a new Scheduler instance.
// monitorEvents and credEvents are receive-only channels from the communication layer.
func NewScheduler(
	monitorEvents <-chan models.Event,
	credEvents <-chan models.Event,
	outputChan chan<- []*models.Monitor,
	fpingPath string,
	tickIntervalSec, fpingTimeoutMs, fpingRetries int,
) *Scheduler {
	return &Scheduler{
		monitors:      make(map[int64]*MonitorWithDeadline),
		creds:         make(map[int64]*models.CredentialProfile),
		monitorEvents: monitorEvents,
		credEvents:    credEvents,
		OutputChan:    outputChan,
		fpingPath:     fpingPath,
		tickInterval:  time.Duration(tickIntervalSec) * time.Second,
		fpingTimeout:  fpingTimeoutMs,
		fpingRetries:  fpingRetries,
	}
}

// LoadCache populates the internal maps and initializes deadlines.
func (sched *Scheduler) LoadCache(monitors []*models.Monitor, creds []*models.CredentialProfile) {
	slog.Info("Loading credentials to cache", "component", "Scheduler", "count", len(creds))
	// Populate sched.creds map
	for _, cred := range creds {
		sched.creds[cred.ID] = cred
	}

	slog.Info("Loading monitors to cache", "component", "Scheduler", "count", len(monitors))
	// Populate sched.monitors map by creating MonitorWithDeadline for each monitor
	now := time.Now()
	for _, mon := range monitors {
		sched.monitors[mon.ID] = &MonitorWithDeadline{
			Monitor:  mon,
			Deadline: now, // Set initial Deadline to now so they're immediately eligible
		}
		slog.Info("Monitor loaded to cache", "component", "Scheduler", "monitor_id", mon.ID, "ip", mon.IPAddress, "interval", mon.PollingIntervalSeconds, "deadline", now.Format(time.RFC3339))
	}
	slog.Info("Cache load complete", "component", "Scheduler", "monitor_count", len(sched.monitors), "credential_count", len(sched.creds))
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

		case event := <-sched.monitorEvents:
			slog.Debug("Received monitor event", "component", "Scheduler", "event_type", event.Type)
			sched.processMonitorEvent(event)

		case event := <-sched.credEvents:
			slog.Debug("Received credential event", "component", "Scheduler", "event_type", event.Type)
			sched.processCredentialEvent(event)

		case <-ticker.C:
			slog.Debug("Tick - running schedule()", "component", "Scheduler")
			sched.schedule()
		}
	}
}

// processMonitorEvent handles CRUD events for monitors.
func (sched *Scheduler) processMonitorEvent(event models.Event) {
	payload, ok := event.Payload.(*models.Monitor)
	if !ok {
		slog.Error("Invalid payload type in monitor event", "component", "Scheduler")
		return
	}

	switch event.Type {
	case models.EventCreate, models.EventUpdate:
		slog.Info("Processing monitor event", "component", "Scheduler", "type", event.Type, "monitor_id", payload.ID, "ip", payload.IPAddress)
		sched.monitors[payload.ID] = &MonitorWithDeadline{
			Monitor:  payload,
			Deadline: time.Now(), // New/updated monitors are immediately eligible
		}
	case models.EventDelete:
		slog.Info("Deleting monitor from cache", "component", "Scheduler", "monitor_id", payload.ID)
		delete(sched.monitors, payload.ID)
	}
}

// processCredentialEvent handles CRUD events for credentials.
func (sched *Scheduler) processCredentialEvent(event models.Event) {
	payload, ok := event.Payload.(*models.CredentialProfile)
	if !ok {
		slog.Error("Invalid payload type in credential event", "component", "Scheduler")
		return
	}

	switch event.Type {
	case models.EventCreate, models.EventUpdate:
		slog.Info("Processing credential event", "component", "Scheduler", "type", event.Type, "credential_id", payload.ID)
		sched.creds[payload.ID] = payload
	case models.EventDelete:
		slog.Info("Deleting credential from cache", "component", "Scheduler", "credential_id", payload.ID)
		delete(sched.creds, payload.ID)
	}
}

// schedule identifies monitors past their deadline, performs batch fping, and updates deadlines.
func (sched *Scheduler) schedule() {
	now := time.Now()
	slog.Debug("Checking deadlines", "component", "Scheduler", "now", now.Format(time.RFC3339))

	// 1. Identify Candidates (those where deadline <= time.Now())
	candidates := make([]*MonitorWithDeadline, 0)
	ips := make([]string, 0)
	ipSet := make(map[string]bool) // Deduplicate IPs

	for _, mwd := range sched.monitors {
		if mwd.Deadline.Before(now) || mwd.Deadline.Equal(now) {
			candidates = append(candidates, mwd)
			// Only add IP to fping list if monitor requires ping check
			if mwd.Monitor.ShouldPing && !ipSet[mwd.Monitor.IPAddress] {
				ips = append(ips, mwd.Monitor.IPAddress)
				ipSet[mwd.Monitor.IPAddress] = true
			}
		}
	}

	slog.Debug("Identified candidate monitors", "component", "Scheduler", "monitor_count", len(candidates), "ip_count", len(ips))

	if len(candidates) == 0 {
		slog.Debug("No candidates due for polling", "component", "Scheduler")
		return
	}

	// 2. Batch fping check on candidate IPs (only those that need it)
	reachableIPs := sched.performBatchFping(ips)
	slog.Debug("Fping results", "component", "Scheduler", "reachable_count", len(reachableIPs), "total_ips", len(ips))

	// 3. Filter qualified monitors and update deadlines
	qualified := make([]*models.Monitor, 0)
	for _, mwd := range candidates {
		// Qualify if: (1) monitor doesn't need ping, OR (2) IP is reachable
		isQualified := !mwd.Monitor.ShouldPing || reachableIPs[mwd.Monitor.IPAddress]

		if isQualified {
			// Attach credential info before sending
			mwd.Monitor.CredentialProfile = sched.creds[mwd.Monitor.CredentialProfileID]
			qualified = append(qualified, mwd.Monitor)

			// Update deadline: new_deadline = current_deadline + interval
			newDeadline := mwd.Deadline.Add(time.Duration(mwd.Monitor.PollingIntervalSeconds) * time.Second)
			mwd.Deadline = newDeadline
			slog.Info("Monitor qualified", "component", "Scheduler", "monitor_id", mwd.Monitor.ID, "should_ping", mwd.Monitor.ShouldPing, "next_deadline", newDeadline.Format(time.RFC3339))
		} else {
			slog.Debug("Monitor not reachable", "component", "Scheduler", "monitor_id", mwd.Monitor.ID, "ip", mwd.Monitor.IPAddress)
		}
	}

	// 4. Dispatch qualified list to OutputChan
	if len(qualified) > 0 {
		slog.Info("Dispatching qualified monitors", "component", "Scheduler", "count", len(qualified))
		sched.OutputChan <- qualified
	} else {
		slog.Debug("No monitors qualified", "component", "Scheduler")
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
