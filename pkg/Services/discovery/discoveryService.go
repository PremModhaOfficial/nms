package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"nms/pkg/api"

	"nms/pkg/models"
	"nms/pkg/plugin"
	"nms/pkg/pluginWorker"
)

// MaxExpandHosts caps how many IPs a single target may expand to (a /16 block).
const MaxExpandHosts = 1 << 16 // 65536

// maxTasksPerBatch caps how many tasks are sent to a single plugin invocation.
// It must stay <= the plugin's own per-batch cap (winrm: maxInputsPerBatch = 10000),
// otherwise a large discovery (e.g. a /16) would be rejected by the plugin and
// silently produce no results. Chunking keeps this invariant without lowering
// MaxExpandHosts.
const maxTasksPerBatch = 10000

// pendingStaleAfter is how long a pending discovery may stay without a result before being reaped.
const pendingStaleAfter = 30 * time.Minute

// discoveryContext holds profile context for pending discoveries
type discoveryContext struct {
	DiscoveryProfileID  int64
	CredentialProfileID int64
	Port                int
	createdAt           time.Time
}

// DiscoveryService coordinates the discovery process.
// It listens for DiscoveryProfile events and manages the DiscoveryPool.
type DiscoveryService struct {
	pool          *pluginWorker.PluginWorkerPool[plugin.Task, plugin.Result]
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
	bufferSize int,
) *DiscoveryService {
	pool := pluginWorker.NewPool[plugin.Task, plugin.Result](workerCount, "DiscoveryPool", bufferSize, "-discovery")
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

	// Start the pluginWorker pool
	discovery.pool.Start(ctx)

	// Start result collector
	go discovery.collectResults(ctx)

	// Main event loop
	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping discovery service", "component", "DiscoveryService")
			return
		case event, ok := <-discovery.events:
			if !ok {
				slog.Info("Discovery events channel closed", "component", "DiscoveryService")
				return
			}
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
		slog.Info("Discovery profile saved", "component", "DiscoveryService", "profile_name", profile.Name)
	case models.EventRunDiscovery:
		slog.Info("Running discovery for profile (explicit trigger)", "component", "DiscoveryService", "profile_name", profile.Name)
		discovery.runDiscovery(ctx, profile)
	case models.EventDelete:
		slog.Info("Profile deleted", "component", "DiscoveryService", "profile_name", profile.Name)
		// Nothing to do - discovery is one-shot
	}
}

// collectResults listens for results from the pluginWorker pool and forwards them.
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
				// Always clear from pending to prevent memory leak
				discovery.pendingMu.Lock()
				dctx, found := discovery.pending[res.Target]
				delete(discovery.pending, res.Target)
				discovery.pendingMu.Unlock()

				// Log and skip failures
				if !res.Success {
					slog.Error("FAILED: Discovery attempt unsuccessful", "component", "DiscoveryService", "target", res.Target, "error", res.Error)
					continue
				}
				if res.Hostname == "" {
					slog.Error("FAILED: No hostname returned", "component", "DiscoveryService", "target", res.Target)
					continue
				}

				// Enrich result with profile context
				if found {
					res.DiscoveryProfileID = dctx.DiscoveryProfileID
					res.CredentialProfileID = dctx.CredentialProfileID
					res.Port = dctx.Port
				}

				slog.Info("SUCCESS: Found device", "component", "DiscoveryService", "hostname", res.Hostname, "target", res.Target)
				select {
				case <-ctx.Done():
					return
				case discovery.resultCh <- res: // Forward to DataWriter
				}
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
		slog.Error("No IPs found for target", "component", "DiscoveryService", "target", profile.Target)
		return
	}
	slog.Info("Expanded target", "component", "DiscoveryService", "target", profile.Target, "ip_count", len(ips))

	// 2. Get credentials (preloaded in event by PreloadingDiscoveryProfileRepo)
	credProfile := profile.CredentialProfile
	if credProfile == nil {
		slog.Error("Credential profile not preloaded in event", "component", "DiscoveryService", "profile_id", profile.CredentialProfileID)
		return
	}

	creds, err := api.DecryptPayload(credProfile, discovery.encryptionKey)
	if err != nil {
		slog.Error("Failed to decrypt credentials, skipping discovery", "component", "DiscoveryService", "credential_id", credProfile.ID, "error", err)
		return
	}

	// 3. Get binary path from protocol
	protocol := credProfile.Protocol
	if protocol == "" {
		slog.Error("No protocol specified in credential profile", "component", "DiscoveryService", "profile_id", profile.CredentialProfileID)
		return
	}

	// Try pluginDir/protocol (standalone) then pluginDir/protocol/protocol (nested)
	binPath := filepath.Join(discovery.pluginDir, protocol)
	info, err := os.Stat(binPath)
	if err != nil || info.IsDir() {
		binPath = filepath.Join(discovery.pluginDir, protocol, protocol)
		info, err = os.Stat(binPath)
		if err != nil || info.IsDir() {
			slog.Error("Plugin binary not found or is a directory", "component", "DiscoveryService", "protocol", protocol, "bin_path", binPath)
			return
		}
	}

	// 4. Register pending discoveries and build tasks
	dctx := discoveryContext{
		DiscoveryProfileID:  profile.ID,
		CredentialProfileID: profile.CredentialProfileID,
		Port:                profile.Port,
		createdAt:           time.Now(),
	}

	discovery.pendingMu.Lock()
	// Reap stale pending entries (targets the plugin never reported back for) to bound the map.
	now := time.Now()
	for ip, pctx := range discovery.pending {
		if now.Sub(pctx.createdAt) > pendingStaleAfter {
			delete(discovery.pending, ip)
		}
	}
	if len(discovery.pending)+len(ips) > MaxExpandHosts {
		discovery.pendingMu.Unlock()
		slog.Error("Too many pending discoveries", "component", "DiscoveryService", "pending", len(discovery.pending), "new", len(ips), "max", MaxExpandHosts)
		return
	}
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

	// 6. Submit to pool in chunks bounded by maxTasksPerBatch, so a single
	// plugin process never receives more than its own batch cap. Submit is
	// ctx-aware, so a stopped pool can never wedge the event loop during shutdown.
	for start := 0; start < len(tasks); start += maxTasksPerBatch {
		end := start + maxTasksPerBatch
		if end > len(tasks) {
			end = len(tasks)
		}
		slog.Info("Submitting tasks to pool", "component", "DiscoveryService", "task_count", end-start, "chunk_start", start, "bin_path", binPath)
		if !discovery.pool.Submit(binPath, tasks[start:end]) {
			slog.Warn("Failed to submit tasks: pool shutting down", "component", "DiscoveryService", "chunk_start", start, "task_count", end-start)
			return
		}
	}
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

	return nil, fmt.Errorf("invalid target %q", target)
}

// expandCIDR expands a CIDR block to all usable host IPs.
// Only IPv4 prefixes of /16 or longer are allowed, so expansion is bounded.
func expandCIDR(cidr string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return nil, fmt.Errorf("IPv6 CIDR %q is not supported for expansion", cidr)
	}
	if ones < 16 {
		return nil, fmt.Errorf("CIDR %q is too large (minimum prefix is /16)", cidr)
	}

	// ones >= 16 guarantees hostCount <= MaxExpandHosts, so this loop terminates.
	hostCount := 1 << (32 - ones)
	ips := make([]string, 0, hostCount)
	for ip := ip.Mask(ipnet.Mask); len(ips) < hostCount; incIP(ip) {
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
		return nil, fmt.Errorf("invalid range %q: expected START-END", rangeStr)
	}

	startIP := net.ParseIP(strings.TrimSpace(parts[0])).To4()
	if startIP == nil {
		return nil, fmt.Errorf("invalid range start %q", parts[0])
	}

	endPart := strings.TrimSpace(parts[1])
	var endIP net.IP
	if parsed := net.ParseIP(endPart); parsed != nil {
		endIP = parsed.To4()
		if endIP == nil {
			return nil, fmt.Errorf("invalid range end %q: must be an IPv4 address", endPart)
		}
	} else {
		// Just the last octet (e.g., "100" in "192.168.1.1-100")
		lastOctet, err := strconv.Atoi(endPart)
		if err != nil || lastOctet < 0 || lastOctet > 255 {
			return nil, fmt.Errorf("invalid range end %q: expected IPv4 address or last octet", endPart)
		}
		endIP = make(net.IP, 4)
		copy(endIP, startIP)
		endIP[3] = byte(lastOctet)
	}

	startN := ipToUint32(startIP)
	endN := ipToUint32(endIP)
	if endN < startN {
		return nil, fmt.Errorf("invalid range %q: end precedes start", rangeStr)
	}
	// Count in uint64 so the full IPv4 space (2^32 hosts) does not overflow.
	hostCount := uint64(endN) - uint64(startN) + 1
	if hostCount > MaxExpandHosts {
		return nil, fmt.Errorf("range %q expands to %d hosts (max %d)", rangeStr, hostCount, MaxExpandHosts)
	}

	ips := make([]string, 0, int(hostCount))
	for ip := copyIP(startIP); len(ips) < int(hostCount); incIP(ip) {
		ips = append(ips, ip.String())
	}
	return ips, nil
}

// ipToUint32 converts a 4-byte IP to a uint32 for bounded counting.
func ipToUint32(ip net.IP) uint32 {
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
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
