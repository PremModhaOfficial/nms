//! Static component graph served to the dashboard (Go tracex/topology.go).
//! Mirrors ARCHITECTURE.md mermaid and the channel wiring in main.rs.

use serde::Serialize;

/// A single component in the topology graph.
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Node {
    pub id: String,
    pub label: String,
    /// "service" or "db".
    pub node_type: String,
}

/// A directed channel/event edge between two nodes.
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Edge {
    pub from: String,
    pub to: String,
    pub label: String,
}

/// Static component graph (10 nodes, 14 edges — exact replica of
/// ARCHITECTURE.md).
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct TopologyGraph {
    pub nodes: Vec<Node>,
    pub edges: Vec<Edge>,
}

/// The static NMS component graph. Node IDs are the exact contract the
/// exporter uses for component_ids.
pub fn topology() -> TopologyGraph {
    TopologyGraph {
        nodes: vec![
            Node { id: "api".into(), label: "API".into(), node_type: "service".into() },
            Node { id: "entity".into(), label: "EntityService".into(), node_type: "service".into() },
            Node { id: "scheduler".into(), label: "Scheduler".into(), node_type: "service".into() },
            Node { id: "poller".into(), label: "Poller".into(), node_type: "service".into() },
            Node { id: "pluginpool".into(), label: "PluginWorkerPool".into(), node_type: "service".into() },
            Node { id: "metrics".into(), label: "MetricsService".into(), node_type: "service".into() },
            Node { id: "db".into(), label: "PostgreSQL".into(), node_type: "db".into() },
            Node { id: "discovery".into(), label: "DiscoveryService".into(), node_type: "service".into() },
            Node { id: "discoverypool".into(), label: "DiscoveryWorkerPool".into(), node_type: "service".into() },
            Node { id: "health".into(), label: "HealthMonitor".into(), node_type: "service".into() },
        ],
        edges: vec![
            Edge { from: "api".into(), to: "entity".into(), label: "Request/Reply".into() },
            Edge { from: "api".into(), to: "metrics".into(), label: "Request/Reply".into() },
            Edge { from: "entity".into(), to: "db".into(), label: "sqlx".into() },
            Edge { from: "entity".into(), to: "scheduler".into(), label: "Events".into() },
            Edge { from: "entity".into(), to: "discovery".into(), label: "Events".into() },
            Edge { from: "scheduler".into(), to: "poller".into(), label: "Devices".into() },
            Edge { from: "scheduler".into(), to: "health".into(), label: "Failures".into() },
            Edge { from: "poller".into(), to: "pluginpool".into(), label: "Jobs".into() },
            Edge { from: "pluginpool".into(), to: "metrics".into(), label: "Results".into() },
            Edge { from: "pluginpool".into(), to: "health".into(), label: "Results".into() },
            Edge { from: "metrics".into(), to: "db".into(), label: "pgx.CopyFrom".into() },
            Edge { from: "health".into(), to: "entity".into(), label: "OpDeactivateDevice".into() },
            Edge { from: "discovery".into(), to: "discoverypool".into(), label: "Jobs".into() },
            Edge { from: "discoverypool".into(), to: "entity".into(), label: "Results".into() },
        ],
    }
}

/// Whether id is a valid topology node id. The exporter uses it to derive
/// component_ids so only real components reach the dashboard.
pub fn is_node_id(id: &str) -> bool {
    matches!(
        id,
        "api" | "entity" | "scheduler" | "poller" | "pluginpool" | "metrics" | "db" | "discovery" | "discoverypool" | "health"
    )
}
