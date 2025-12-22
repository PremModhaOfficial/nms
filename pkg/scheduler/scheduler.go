package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"log"
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
func (s *Scheduler) LoadCache(monitors []*models.Monitor, creds []*models.CredentialProfile) {
	log.Printf("[Scheduler] LoadCache: Loading %d credentials", len(creds))
	// Populate s.creds map
	for _, cred := range creds {
		s.creds[cred.ID] = cred
	}

	log.Printf("[Scheduler] LoadCache: Loading %d monitors", len(monitors))
	// Populate s.monitors map by creating MonitorWithDeadline for each monitor
	now := time.Now()
	for _, mon := range monitors {
		s.monitors[mon.ID] = &MonitorWithDeadline{
			Monitor:  mon,
			Deadline: now, // Set initial Deadline to now so they're immediately eligible
		}
		log.Printf("[Scheduler] LoadCache: Monitor ID=%d, IP=%s, Interval=%ds, Deadline=%s",
			mon.ID, mon.IPAddress, mon.PollingIntervalSeconds, now.Format(time.RFC3339))
	}
	log.Printf("[Scheduler] LoadCache: Complete. %d monitors, %d credentials loaded", len(s.monitors), len(s.creds))
}

// Run starts the main loop.
func (s *Scheduler) Run(ctx context.Context) {
	log.Printf("[Scheduler] Run: Starting main loop with tick interval=%s", s.tickInterval)
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Scheduler] Run: Context cancelled, shutting down")
			return

		case event := <-s.monitorEvents:
			log.Printf("[Scheduler] Run: Received monitor event type=%s", event.Type)
			s.processMonitorEvent(event)

		case event := <-s.credEvents:
			log.Printf("[Scheduler] Run: Received credential event type=%s", event.Type)
			s.processCredentialEvent(event)

		case <-ticker.C:
			log.Println("[Scheduler] Run: Tick - running schedule()")
			s.schedule()
		}
	}
}

// processMonitorEvent handles CRUD events for monitors.
func (s *Scheduler) processMonitorEvent(event models.Event) {
	payload, ok := event.Payload.(*models.Monitor)
	if !ok {
		log.Printf("[Scheduler] processMonitorEvent: Invalid payload type")
		return
	}

	switch event.Type {
	case models.EventCreate, models.EventUpdate:
		log.Printf("[Scheduler] processMonitorEvent: %s Monitor ID=%d, IP=%s", event.Type, payload.ID, payload.IPAddress)
		s.monitors[payload.ID] = &MonitorWithDeadline{
			Monitor:  payload,
			Deadline: time.Now(), // New/updated monitors are immediately eligible
		}
	case models.EventDelete:
		log.Printf("[Scheduler] processMonitorEvent: Delete Monitor ID=%d", payload.ID)
		delete(s.monitors, payload.ID)
	}
}

// processCredentialEvent handles CRUD events for credentials.
func (s *Scheduler) processCredentialEvent(event models.Event) {
	payload, ok := event.Payload.(*models.CredentialProfile)
	if !ok {
		log.Printf("[Scheduler] processCredentialEvent: Invalid payload type")
		return
	}

	switch event.Type {
	case models.EventCreate, models.EventUpdate:
		log.Printf("[Scheduler] processCredentialEvent: %s CredentialProfile ID=%d", event.Type, payload.ID)
		s.creds[payload.ID] = payload
	case models.EventDelete:
		log.Printf("[Scheduler] processCredentialEvent: Delete CredentialProfile ID=%d", payload.ID)
		delete(s.creds, payload.ID)
	}
}

// schedule identifies monitors past their deadline, performs batch fping, and updates deadlines.
func (s *Scheduler) schedule() {
	now := time.Now()
	log.Printf("[Scheduler] schedule: Checking deadlines at %s", now.Format(time.RFC3339))

	// 1. Identify Candidates (those where deadline <= time.Now())
	candidates := make([]*MonitorWithDeadline, 0)
	ips := make([]string, 0)
	ipSet := make(map[string]bool) // Deduplicate IPs

	for _, mwd := range s.monitors {
		if mwd.Deadline.Before(now) || mwd.Deadline.Equal(now) {
			candidates = append(candidates, mwd)
			if !ipSet[mwd.Monitor.IPAddress] {
				ips = append(ips, mwd.Monitor.IPAddress)
				ipSet[mwd.Monitor.IPAddress] = true
			}
		}
	}

	log.Printf("[Scheduler] schedule: Found %d candidate monitors with %d unique IPs", len(candidates), len(ips))

	if len(candidates) == 0 {
		log.Println("[Scheduler] schedule: No candidates due for polling")
		return
	}

	// 2. Batch fping check on candidate IPs
	reachableIPs := s.performBatchFping(ips)
	log.Printf("[Scheduler] schedule: Fping results: %d/%d IPs reachable", len(reachableIPs), len(ips))

	// 3. Filter qualified monitors and update deadlines
	qualified := make([]*models.Monitor, 0)
	for _, mwd := range candidates {
		if reachableIPs[mwd.Monitor.IPAddress] {
			// Attach credential info before sending
			mwd.Monitor.CredentialProfile = s.creds[mwd.Monitor.CredentialProfileID]
			qualified = append(qualified, mwd.Monitor)

			// Update deadline: new_deadline = current_deadline + interval
			newDeadline := mwd.Deadline.Add(time.Duration(mwd.Monitor.PollingIntervalSeconds) * time.Second)
			mwd.Deadline = newDeadline
			log.Printf("[Scheduler] schedule: Monitor ID=%d qualified, next deadline=%s",
				mwd.Monitor.ID, newDeadline.Format(time.RFC3339))
		} else {
			log.Printf("[Scheduler] schedule: Monitor ID=%d IP=%s not reachable, skipping",
				mwd.Monitor.ID, mwd.Monitor.IPAddress)
		}
	}

	// 4. Dispatch qualified list to OutputChan
	if len(qualified) > 0 {
		log.Printf("[Scheduler] schedule: Dispatching %d qualified monitors to OutputChan", len(qualified))
		s.OutputChan <- qualified
	} else {
		log.Println("[Scheduler] schedule: No monitors qualified (all unreachable)")
	}
}

// performBatchFping runs fping against a list of IPs and returns reachability.
func (s *Scheduler) performBatchFping(ips []string) map[string]bool {
	reachable := make(map[string]bool)

	if len(ips) == 0 {
		log.Println("[Scheduler] performBatchFping: No IPs to check")
		return reachable
	}

	log.Printf("[Scheduler] performBatchFping: Checking %d IPs with fping (timeout=%dms, retries=%d)",
		len(ips), s.fpingTimeout, s.fpingRetries)

	// Build fping command
	// -a: show alive hosts
	// -q: quiet (don't show per-target results)
	// -t: timeout in ms
	// -r: retry count
	args := []string{
		"-a",
		"-q",
		"-t", fmt.Sprintf("%d", s.fpingTimeout),
		"-r", fmt.Sprintf("%d", s.fpingRetries),
	}
	args = append(args, ips...)

	cmd := exec.Command(s.fpingPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	// fping returns non-zero if some hosts are unreachable, so we don't treat that as an error
	if err != nil {
		log.Printf("[Scheduler] performBatchFping: fping exited with: %v (this is normal if some hosts are down)", err)
	}

	// Parse stdout for reachable IPs (one per line)
	output := strings.TrimSpace(stdout.String())
	if output != "" {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			ip := strings.TrimSpace(line)
			if ip != "" {
				reachable[ip] = true
				log.Printf("[Scheduler] performBatchFping: IP %s is reachable", ip)
			}
		}
	}

	log.Printf("[Scheduler] performBatchFping: Complete. %d/%d IPs reachable", len(reachable), len(ips))
	return reachable
}
