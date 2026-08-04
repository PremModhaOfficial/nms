package tracex

// TopologyGraph is the static component graph served to the dashboard. It
// mirrors the ARCHITECTURE.md mermaid diagram and the channel wiring in
// cmd/app/main.go initServices (10 nodes, 14 edges).
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
// The graph replicates the ARCHITECTURE.md mermaid diagram: API, EntityService,
// Scheduler, Poller, PluginWorkerPool, MetricsService, PostgreSQL, DiscoveryService,
// DiscoveryWorkerPool, HealthMonitor. Edges map to the channel wiring in
// cmd/app/main.go initServices (crudRequestChan, provisioningEventChan,
// metricRequestChan, deviceChan, discProfileChan, discResultChan,
// schedulerToPollerChan, failureChan, pollResultChan, OpDeactivateDevice) plus
// the discovery pool's Jobs/Results hops.
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
			{ID: "discoverypool", Label: "DiscoveryWorkerPool", Type: "service"},
			{ID: "health", Label: "HealthMonitor", Type: "service"},
		},
		Edges: []Edge{
			// Mirrors ARCHITECTURE.md mermaid exactly (10 nodes, 14 edges).
			{From: "api", To: "entity", Label: "Request/Reply"},
			{From: "api", To: "metrics", Label: "Request/Reply"},
			{From: "entity", To: "db", Label: "sqlx"},
			{From: "entity", To: "scheduler", Label: "Events"},
			{From: "entity", To: "discovery", Label: "Events"},
			{From: "scheduler", To: "poller", Label: "Devices"},
			{From: "scheduler", To: "health", Label: "Failures"},
			{From: "poller", To: "pluginpool", Label: "Jobs"},
			{From: "pluginpool", To: "metrics", Label: "Results"},
			{From: "pluginpool", To: "health", Label: "Results"},
			{From: "metrics", To: "db", Label: "pgx.CopyFrom"},
			{From: "health", To: "entity", Label: "OpDeactivateDevice"},
			{From: "discovery", To: "discoverypool", Label: "Jobs"},
			{From: "discoverypool", To: "entity", Label: "Results"},
		},
	}
}

// isNodeID reports whether id is a valid topology node id. The exporter uses
// it to derive ComponentIDs so only real components reach the dashboard.
func isNodeID(id string) bool {
	switch id {
	case "api", "entity", "scheduler", "poller", "pluginpool", "metrics", "db", "discovery", "discoverypool", "health":
		return true
	default:
		return false
	}
}
