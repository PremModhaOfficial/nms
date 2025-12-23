package discovery

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"nms/pkg/database"
	"nms/pkg/models"
	"nms/pkg/plugin"
	"nms/pkg/worker"
)

// discoveryContext holds profile context for pending discoveries
type discoveryContext struct {
	DiscoveryProfileID  int64
	CredentialProfileID int64
	Port                int
}

// DiscoveryService coordinates the discovery process.
// It listens for DiscoveryProfile events and manages the DiscoveryPool.
type DiscoveryService struct {
	pool          *worker.Pool[plugin.Task, plugin.Result]
	events        <-chan models.Event  // Reads discovery profile events
	resultCh      chan<- plugin.Result // Writes discovery results
	pluginDir     string
	encryptionKey string

	// Tracks pending discoveries: target IP -> context
	pendingMu sync.RWMutex
	pending   map[string]discoveryContext
}

// NewDiscoveryService creates a new discovery service.
func NewDiscoveryService(
	events <-chan models.Event,
	resultCh chan<- plugin.Result,
	pluginDir string,
	encryptionKey string,
	workerCount int,
) *DiscoveryService {
	pool := worker.NewPool[plugin.Task, plugin.Result](workerCount, "DiscoveryPool", "-discovery")
	return &DiscoveryService{
		events:        events,
		pool:          pool,
		resultCh:      resultCh,
		pluginDir:     pluginDir,
		encryptionKey: encryptionKey,
		pending:       make(map[string]discoveryContext),
	}
}

// Start initiates the discovery event processor and result collector.
func (discovery *DiscoveryService) Start(ctx context.Context) {
	slog.Info("Starting discovery service", "component", "DiscoveryService")

	// Start the worker pool
	discovery.pool.Start(ctx)

	// Start result collector
	go discovery.collectResults(ctx)

	// Main event loop
	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping discovery service", "component", "DiscoveryService")
			return
		case event := <-discovery.events:
			discovery.processEvent(ctx, event)
		}
	}
}

// processEvent handles CRUD events for DiscoveryProfiles.
func (discovery *DiscoveryService) processEvent(ctx context.Context, event models.Event) {
	profile, ok := event.Payload.(*models.DiscoveryProfile)
	if !ok {
		slog.Warn("Ignoring event with unexpected payload type", "component", "DiscoveryService")
		return
	}

	switch event.Type {
	case models.EventCreate, models.EventUpdate:
		slog.Info("Running discovery for profile", "component", "DiscoveryService", "profile_name", profile.Name)
		discovery.runDiscovery(ctx, profile)
	case models.EventDelete:
		slog.Info("Profile deleted", "component", "DiscoveryService", "profile_name", profile.Name)
		// Nothing to do - discovery is one-shot
	}
}

// collectResults listens for results from the worker pool and forwards them.
func (discovery *DiscoveryService) collectResults(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case results, ok := <-discovery.pool.Results():
			if !ok {
				return
			}
			for _, res := range results {
				if !res.Success || res.Hostname == "" {
					continue
				}

				// Enrich result with profile context
				discovery.pendingMu.RLock()
				dctx, found := discovery.pending[res.Target]
				discovery.pendingMu.RUnlock()

				if found {
					res.DiscoveryProfileID = dctx.DiscoveryProfileID
					res.CredentialProfileID = dctx.CredentialProfileID
					res.Port = dctx.Port

					slog.Info("SUCCESS: Found device", "component", "DiscoveryService", "hostname", res.Hostname, "target", res.Target)

					// Clear from pending
					discovery.pendingMu.Lock()
					delete(discovery.pending, res.Target)
					discovery.pendingMu.Unlock()
				}

				discovery.resultCh <- res // Forward to DataWriter
			}
		}
	}
}

// runDiscovery expands the profile target and submits tasks to the pool.
func (discovery *DiscoveryService) runDiscovery(ctx context.Context, profile *models.DiscoveryProfile) {
	// 1. Expand target to individual IPs
	ips, err := expandTarget(profile.Target)
	if err != nil {
		slog.Error("Failed to expand target", "component", "DiscoveryService", "target", profile.Target, "error", err)
		return
	}
	if len(ips) == 0 {
		slog.Warn("No IPs found for target", "component", "DiscoveryService", "target", profile.Target)
		return
	}
	slog.Info("Expanded target", "component", "DiscoveryService", "target", profile.Target, "ip_count", len(ips))

	// 2. Get credentials (preloaded in event by PreloadingDiscoveryProfileRepo)
	credProfile := profile.CredentialProfile

	creds, err := database.DecryptPayload(credProfile, discovery.encryptionKey)
	if err != nil {
		slog.Error("Failed to decrypt credentials", "component", "DiscoveryService", "error", err)
		creds = ""
	}

	// 3. Get binary path from protocol
	var protocol string
	if credProfile != nil {
		protocol = credProfile.Protocol
	}

	// Try pluginDir/protocol (standalone) then pluginDir/protocol/protocol (nested)
	binPath := filepath.Join(discovery.pluginDir, protocol)
	if _, err := os.Stat(binPath); err != nil {
		binPath = filepath.Join(discovery.pluginDir, protocol, protocol)
		if _, err := os.Stat(binPath); err != nil {
			slog.Error("Plugin not found for protocol", "component", "DiscoveryService", "protocol", protocol)
			return
		}
	}

	// 4. Register pending discoveries and build tasks
	dctx := discoveryContext{
		DiscoveryProfileID:  profile.ID,
		CredentialProfileID: profile.CredentialProfileID,
		Port:                profile.Port,
	}

	discovery.pendingMu.Lock()
	for _, ip := range ips {
		discovery.pending[ip] = dctx
	}
	discovery.pendingMu.Unlock()

	// 5. Build tasks
	tasks := make([]plugin.Task, 0, len(ips))
	for _, ip := range ips {
		tasks = append(tasks, plugin.Task{
			Target:      ip,
			Port:        profile.Port,
			Credentials: creds,
		})
	}

	// 6. Submit to pool
	slog.Info("Submitting tasks to pool", "component", "DiscoveryService", "task_count", len(tasks), "bin_path", binPath)
	discovery.pool.Submit(binPath, tasks)
}

// expandTarget expands a target string to individual IPs.
// Supports: single IP, CIDR notation, IP ranges (start-end).
func expandTarget(target string) ([]string, error) {
	target = strings.TrimSpace(target)

	// Check for CIDR notation
	if strings.Contains(target, "/") {
		return expandCIDR(target)
	}

	// Check for range notation (e.g., 192.168.1.1-192.168.1.100 or 192.168.1.1-100)
	if strings.Contains(target, "-") {
		return expandRange(target)
	}

	// Single IP
	if net.ParseIP(target) != nil {
		return []string{target}, nil
	}

	return nil, nil
}

// expandCIDR expands a CIDR block to all usable host IPs.
func expandCIDR(cidr string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	var ips []string
	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); incIP(ip) {
		ips = append(ips, ip.String())
	}

	// Remove network and broadcast addresses for typical subnets
	if len(ips) > 2 {
		ips = ips[1 : len(ips)-1]
	}
	return ips, nil
}

// expandRange expands an IP range like "192.168.1.1-192.168.1.100" or "192.168.1.1-100".
func expandRange(rangeStr string) ([]string, error) {
	parts := strings.Split(rangeStr, "-")
	if len(parts) != 2 {
		return nil, nil
	}

	startIP := net.ParseIP(strings.TrimSpace(parts[0]))
	if startIP == nil {
		return nil, nil
	}
	startIP = startIP.To4()
	if startIP == nil {
		return nil, nil
	}

	endPart := strings.TrimSpace(parts[1])

	// Check if end is full IP or just last octet
	var endIP net.IP
	if net.ParseIP(endPart) != nil {
		endIP = net.ParseIP(endPart).To4()
	} else {
		// Just the last octet (e.g., "100" in "192.168.1.1-100")
		lastOctet, err := strconv.Atoi(endPart)
		if err != nil || lastOctet < 0 || lastOctet > 255 {
			return nil, nil
		}
		endIP = make(net.IP, 4)
		copy(endIP, startIP)
		endIP[3] = byte(lastOctet)
	}

	if endIP == nil {
		return nil, nil
	}

	var ips []string
	for ip := copyIP(startIP); compareIP(ip, endIP) <= 0; incIP(ip) {
		ips = append(ips, ip.String())
	}
	return ips, nil
}

// incIP increments an IP address by one.
func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// copyIP creates a copy of an IP address.
func copyIP(ip net.IP) net.IP {
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	return dup
}

// compareIP compares two IPs. Returns -1, 0, or 1.
func compareIP(a, b net.IP) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
