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

// DeviceWithDeadline combines a device with its next scheduled polling time.
type DeviceWithDeadline struct {
	Device   *models.Device
	Deadline time.Time
}

// Scheduler manages the scheduling of devices based on deadlines.
type Scheduler struct {
	// Cache maps
	devices     map[int64]*DeviceWithDeadline
	credentials map[int64]*models.CredentialProfile

	// Channels - received from outside for event-driven communication
	deviceEvents <-chan models.Event     // Device CRUD events
	credEvents   <-chan models.Event     // Credential CRUD events
	OutputChan   chan<- []*models.Device // Sends qualified devices to poller

	// Config
	fpingPath    string
	tickInterval time.Duration
	fpingTimeout int
	fpingRetries int
}

// NewScheduler creates a new Scheduler instance.
// monitorEvents and credEvents are receive-only channels from the communication layer.
func NewScheduler(
	deviceEvents <-chan models.Event,
	credEvents <-chan models.Event,
	outputChan chan<- []*models.Device,
	fpingPath string,
	tickIntervalSec, fpingTimeoutMs, fpingRetries int,
) *Scheduler {
	return &Scheduler{
		devices:      make(map[int64]*DeviceWithDeadline),
		credentials:  make(map[int64]*models.CredentialProfile),
		deviceEvents: deviceEvents,
		credEvents:   credEvents,
		OutputChan:   outputChan,
		fpingPath:    fpingPath,
		tickInterval: time.Duration(tickIntervalSec) * time.Second,
		fpingTimeout: fpingTimeoutMs,
		fpingRetries: fpingRetries,
	}
}

// LoadCache populates the internal maps and initializes deadlines.
func (sched *Scheduler) LoadCache(devices []*models.Device, creds []*models.CredentialProfile) {
	slog.Info("Loading credentials to cache", "component", "Scheduler", "count", len(creds))
	// Populate sched.credentials map
	for _, cred := range creds {
		sched.credentials[cred.ID] = cred
	}

	slog.Info("Loading devices to cache", "component", "Scheduler", "count", len(devices))
	// Populate sched.devices map by creating DeviceWithDeadline for each device
	now := time.Now()
	for _, dev := range devices {
		sched.devices[dev.ID] = &DeviceWithDeadline{
			Device:   dev,
			Deadline: now, // Set initial Deadline to now so they're immediately eligible
		}
		slog.Info("Device loaded to cache", "component", "Scheduler", "device_id", dev.ID, "ip", dev.IPAddress, "interval", dev.PollingIntervalSeconds, "deadline", now.Format(time.RFC3339))
	}
	slog.Info("Cache load complete", "component", "Scheduler", "device_count", len(sched.devices), "credential_count", len(sched.credentials))
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

		case event := <-sched.credEvents:
			slog.Debug("Received credential event", "component", "Scheduler", "event_type", event.Type)
			sched.processCredentialEvent(event)

		case <-ticker.C:
			slog.Debug("Tick - running schedule()", "component", "Scheduler")
			sched.schedule()
		}
	}
}

// processDeviceEvent handles CRUD events for devices.
func (sched *Scheduler) processDeviceEvent(event models.Event) {
	payload, ok := event.Payload.(*models.Device)
	if !ok {
		slog.Error("Invalid payload type in device event", "component", "Scheduler")
		return
	}

	switch event.Type {
	case models.EventCreate, models.EventUpdate:
		slog.Info("Processing device event", "component", "Scheduler", "type", event.Type, "device_id", payload.ID, "ip", payload.IPAddress)
		sched.devices[payload.ID] = &DeviceWithDeadline{
			Device:   payload,
			Deadline: time.Now(), // New/updated devices are immediately eligible
		}
	case models.EventDelete:
		slog.Info("Deleting device from cache", "component", "Scheduler", "device_id", payload.ID)
		delete(sched.devices, payload.ID)
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
		sched.credentials[payload.ID] = payload
	case models.EventDelete:
		slog.Info("Deleting credential from cache", "component", "Scheduler", "credential_id", payload.ID)
		delete(sched.credentials, payload.ID)
	}
}

// schedule identifies monitors past their deadline, performs batch fping, and updates deadlines.
func (sched *Scheduler) schedule() {
	now := time.Now()
	slog.Debug("Checking deadlines", "component", "Scheduler", "now", now.Format(time.RFC3339))

	// 1. Identify Candidates (those where deadline <= time.Now())
	candidates := make([]*DeviceWithDeadline, 0)
	ips := make([]string, 0)
	ipSet := make(map[string]bool) // Deduplicate IPs

	for _, dwd := range sched.devices {
		if dwd.Deadline.Before(now) || dwd.Deadline.Equal(now) {
			candidates = append(candidates, dwd)
			// Only add IP to fping list if device requires ping check
			if dwd.Device.ShouldPing && !ipSet[dwd.Device.IPAddress] {
				ips = append(ips, dwd.Device.IPAddress)
				ipSet[dwd.Device.IPAddress] = true
			}
		}
	}

	slog.Debug("Identified candidate devices", "component", "Scheduler", "device_count", len(candidates), "ip_count", len(ips))

	if len(candidates) == 0 {
		slog.Debug("No candidates due for polling", "component", "Scheduler")
		return
	}

	// 2. Batch fping check on candidate IPs (only those that need it)
	reachableIPs := sched.performBatchFping(ips)
	slog.Debug("Fping results", "component", "Scheduler", "reachable_count", len(reachableIPs), "total_ips", len(ips))

	// 3. Filter qualified devices and update deadlines
	qualified := make([]*models.Device, 0)
	for _, dwd := range candidates {
		// Qualify if: (1) device doesn't need ping, OR (2) IP is reachable
		isQualified := !dwd.Device.ShouldPing || reachableIPs[dwd.Device.IPAddress]

		if isQualified {
			// Attach credential info before sending
			dwd.Device.CredentialProfile = sched.credentials[dwd.Device.CredentialProfileID]
			qualified = append(qualified, dwd.Device)

			// Update deadline: new_deadline = current_deadline + interval
			newDeadline := dwd.Deadline.Add(time.Duration(dwd.Device.PollingIntervalSeconds) * time.Second)
			dwd.Deadline = newDeadline
			slog.Info("Device qualified", "component", "Scheduler", "device_id", dwd.Device.ID, "should_ping", dwd.Device.ShouldPing, "next_deadline", newDeadline.Format(time.RFC3339))
		} else {
			slog.Debug("Device not reachable", "component", "Scheduler", "device_id", dwd.Device.ID, "ip", dwd.Device.IPAddress)
		}
	}

	// 4. Dispatch qualified list to OutputChan
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
		slog.Debug("fping exited with error (normal if some hosts down)", "component", "Scheduler", "error", err) // TODO fix logs
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
