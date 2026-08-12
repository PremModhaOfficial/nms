//! Provisioning handlers (Go pkg/api/provisioning.go). Implemented by the api agent.

pub struct ProvisionRequest {
    pub polling_interval_seconds: i32,
}

pub fn run_discovery_handler(
    _event_chan: tokio::sync::mpsc::Sender<crate::models::Event>,
    _crud_req_ch: tokio::sync::mpsc::Sender<crate::models::Request>,
) -> axum::routing::MethodRouter {
    todo!("implemented by api agent")
}

pub fn provision_device_handler(
    _provision_ch: tokio::sync::mpsc::Sender<crate::models::Event>,
) -> axum::routing::MethodRouter {
    todo!("implemented by api agent")
}
