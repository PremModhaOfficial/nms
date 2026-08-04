package tracex

// TopologyGraph is the static component graph served to the dashboard. It
// mirrors ARCHITECTURE.md and the channel wiring in cmd/app/main.go initServices.
//
// Note: the Go type is TopologyGraph because Go shares one namespace for types
// and functions; Topology() is the accessor.
type TopologyGraph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Node is a single component in the topology graph.
type Node struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"` // "service" or "db"
}

// Edge is a directed channel/event edge between two nodes.
type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

// Topology returns the static NMS component graph. Node IDs are the exact
// contract the exporter uses for ComponentIDs.
//
// Edges settled from initServices (channel producer -> consumer):
//   - crudRequestChan: API -> EntityService (also read by Scheduler/Poller/HealthMonitor)
//   - provisioningEventChan: API -> EntityService (carries trigger_discovery AND
//     provision_device; EntityService consumes it, NOT Discovery or HealthMonitor)
//   - metricRequestChan: API -> MetricsService
//   - deviceChan: EntityService -> Scheduler
//   - discProfileChan: EntityService -> DiscoveryService (EventRunDiscovery)
//   - discResultChan: DiscoveryService -> EntityService (results)
//   - schedulerToPollerChan: Scheduler -> Poller
//   - failureChan: Scheduler + MetricsService -> HealthMonitor
//   - pollResultChan: Poller -> MetricsService
//   - OpDeactivateDevice: HealthMonitor -> EntityService (via crudRequestChan)
func Topology() TopologyGraph {
	return TopologyGraph{
		Nodes: []Node{
			{ID: "api", Label: "API", Type: "service"},
			{ID: "entity", Label: "EntityService", Type: "service"},
			{ID: "scheduler", Label: "Scheduler", Type: "service"},
			{ID: "poller", Label: "Poller", Type: "service"},
			{ID: "pluginpool", Label: "PluginWorkerPool", Type: "service"},
			{ID: "metrics", Label: "MetricsService", Type: "service"},
			{ID: "db", Label: "PostgreSQL", Type: "db"},
			{ID: "discovery", Label: "DiscoveryService", Type: "service"},
			{ID: "health", Label: "HealthMonitor", Type: "service"},
		},
		Edges: []Edge{
			{From: "api", To: "entity", Label: "crudRequest"},
			{From: "api", To: "entity", Label: "event.publish"}, // provisioningEventChan: trigger_discovery + provision_device
			{From: "api", To: "metrics", Label: "metricRequest"},
			{From: "entity", To: "db", Label: "sqlx"},
			{From: "entity", To: "scheduler", Label: "deviceEvents"},
			{From: "entity", To: "discovery", Label: "run_discovery"}, // discProfileChan
			{From: "discovery", To: "entity", Label: "discResult"},    // discResultChan
			{From: "discovery", To: "pluginpool", Label: "jobs"},
			{From: "scheduler", To: "poller", Label: "schedulerToPollerChan"},
			{From: "scheduler", To: "health", Label: "failureChan"},
			{From: "poller", To: "pluginpool", Label: "jobs"},
			{From: "pluginpool", To: "metrics", Label: "pollResultChan"},
			{From: "pluginpool", To: "health", Label: "failureChan"}, // failure via metrics
			{From: "metrics", To: "db", Label: "pgx.CopyFrom"},
			{From: "health", To: "entity", Label: "OpDeactivateDevice"},
		},
	}
}

// isNodeID reports whether id is a valid topology node id. The exporter uses
// it to derive ComponentIDs so only real components reach the dashboard.
func isNodeID(id string) bool {
	switch id {
	case "api", "entity", "scheduler", "poller", "pluginpool", "metrics", "db", "discovery", "health":
		return true
	default:
		return false
	}
}
