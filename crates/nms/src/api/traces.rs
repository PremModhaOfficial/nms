//! Dashboard trace/topology handlers (Go pkg/api/traces.go). Implemented by
//! the api agent.

pub fn topology_handler() -> axum::routing::MethodRouter {
    todo!("implemented by api agent")
}

pub fn traces_list_handler(_tracer: std::sync::Arc<crate::tracex::Tracer>) -> axum::routing::MethodRouter {
    todo!("implemented by api agent")
}

pub fn trace_get_handler(_tracer: std::sync::Arc<crate::tracex::Tracer>) -> axum::routing::MethodRouter {
    todo!("implemented by api agent")
}
