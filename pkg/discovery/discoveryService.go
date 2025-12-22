package discovery

import (
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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
	pool      *worker.Pool[plugin.Task, plugin.Result]
	events    <-chan models.Event  // Reads discovery profile events
	resultCh  chan<- plugin.Result // Writes discovery results
	pluginDir string

	// Tracks pending discoveries: target IP -> context
	pendingMu sync.RWMutex
	pending   map[string]discoveryContext
}

// NewDiscoveryService creates a new discovery service.
func NewDiscoveryService(
	events <-chan models.Event,
	resultCh chan<- plugin.Result,
	pluginDir string,
	workerCount int,
) *DiscoveryService {
	pool := worker.NewPool[plugin.Task, plugin.Result](workerCount, "DiscoveryPool", "-discovery")
	return &DiscoveryService{
		events:    events,
		pool:      pool,
		resultCh:  resultCh,
		pluginDir: pluginDir,
		pending:   make(map[string]discoveryContext),
	}
}

// Start initiates the discovery event processor and result collector.
func (s *DiscoveryService) Start(ctx context.Context) {
	log.Println("[DiscoveryService] Starting service")

	// Start the worker pool
	s.pool.Start(ctx)

	// Start result collector
	go s.collectResults(ctx)

	// Main event loop
	for {
		select {
		case <-ctx.Done():
			log.Println("[DiscoveryService] Stopping service")
			return
		case event := <-s.events:
			s.processEvent(ctx, event)
		}
	}
}

// processEvent handles CRUD events for DiscoveryProfiles.
func (s *DiscoveryService) processEvent(ctx context.Context, event models.Event) {
	profile, ok := event.Payload.(*models.DiscoveryProfile)
	if !ok {
		log.Printf("[DiscoveryService] Ignoring event with unexpected payload type")
		return
	}

	switch event.Type {
	case models.EventCreate, models.EventUpdate:
		log.Printf("[DiscoveryService] Running discovery for profile: %s", profile.Name)
		s.runDiscovery(ctx, profile)
	case models.EventDelete:
		log.Printf("[DiscoveryService] Profile deleted: %s", profile.Name)
		// Nothing to do - discovery is one-shot
	}
}

// collectResults listens for results from the worker pool and forwards them.
func (s *DiscoveryService) collectResults(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case results, ok := <-s.pool.Results():
			if !ok {
				return
			}
			for _, res := range results {
				if !res.Success || res.Hostname == "" {
					continue
				}

				// Enrich result with profile context
				s.pendingMu.RLock()
				dctx, found := s.pending[res.Target]
				s.pendingMu.RUnlock()

				if found {
					res.DiscoveryProfileID = dctx.DiscoveryProfileID
					res.CredentialProfileID = dctx.CredentialProfileID
					res.Port = dctx.Port

					log.Printf("[DiscoveryService] SUCCESS: Found device %s at %s", res.Hostname, res.Target)

					// Clear from pending
					s.pendingMu.Lock()
					delete(s.pending, res.Target)
					s.pendingMu.Unlock()
				}

				s.resultCh <- res // Forward to DataWriter
			}
		}
	}
}

// runDiscovery expands the profile target and submits tasks to the pool.
func (s *DiscoveryService) runDiscovery(ctx context.Context, profile *models.DiscoveryProfile) {
	// 1. Expand target to individual IPs
	ips, err := expandTarget(profile.Target)
	if err != nil {
		log.Printf("[DiscoveryService] Failed to expand target %s: %v", profile.Target, err)
		return
	}
	if len(ips) == 0 {
		log.Printf("[DiscoveryService] No IPs found for target: %s", profile.Target)
		return
	}
	log.Printf("[DiscoveryService] Expanded %s to %d IPs", profile.Target, len(ips))

	// 2. Get credentials (preloaded in event by PreloadingDiscoveryProfileRepo)
	credProfile := profile.CredentialProfile

	creds, err := plugin.DecryptPayload(credProfile)
	if err != nil {
		log.Printf("[DiscoveryService] Failed to decrypt credentials: %v", err)
		creds = ""
	}

	// 3. Get binary path from protocol
	protocol := "winrm" // default
	if credProfile != nil {
		protocol = credProfile.Protocol
	}

	// Try pluginDir/protocol (standalone) then pluginDir/protocol/protocol (nested)
	binPath := filepath.Join(s.pluginDir, protocol)
	if _, err := os.Stat(binPath); err != nil {
		binPath = filepath.Join(s.pluginDir, protocol, protocol)
		if _, err := os.Stat(binPath); err != nil {
			log.Printf("[DiscoveryService] Plugin not found for protocol %s (tried %s and %s/%s/%s)",
				protocol,
				filepath.Join(s.pluginDir, protocol),
				s.pluginDir, protocol, protocol)
			return
		}
	}

	// 4. Register pending discoveries and build tasks
	dctx := discoveryContext{
		DiscoveryProfileID:  profile.ID,
		CredentialProfileID: profile.CredentialProfileID,
		Port:                profile.Port,
	}

	s.pendingMu.Lock()
	for _, ip := range ips {
		s.pending[ip] = dctx
	}
	s.pendingMu.Unlock()

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
	log.Printf("[DiscoveryService] Submitting %d tasks to pool using %s", len(tasks), binPath)
	s.pool.Submit(binPath, tasks)
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
