//! Entity CRUD + metrics handlers (Go pkg/api/routes.go). Implemented by the
//! api agent. Stub compiles; agent replaces bodies.

use axum::Router;
use tokio::sync::mpsc;

use crate::config::Config;
use crate::models::Request;
use crate::tracex::Tracer;

/// Request channels shared by API handlers (Go apiChannels).
#[derive(Clone)]
pub struct ApiChannels {
    pub crud_request: mpsc::Sender<Request>,
    pub metric_request: mpsc::Sender<Request>,
    pub provisioning_event: mpsc::Sender<crate::models::Event>,
}

/// Assemble the full axum router (routes + middleware stack).
pub fn build_router(
    _cfg: &Config,
    _auth: crate::api::jwt::JwtAuth,
    _channels: ApiChannels,
    _tracer: std::sync::Arc<Tracer>,
) -> Router {
    todo!("implemented by api agent")
}

/// Bounded request-reply RPC (5s rpc_timeout; Go doRequest).
pub async fn do_request(
    _req_ch: &mpsc::Sender<Request>,
    _operation: crate::models::Operation,
    _entity_type: crate::models::EntityType,
    _id: Option<i64>,
    _payload: crate::models::RequestPayload,
) -> Result<crate::models::Response, crate::api::ApiError> {
    todo!("implemented by api agent")
}

/// Maps a service-layer error to an ApiError (Go classifyError).
pub fn classify_error(_err: &dyn std::error::Error) -> crate::api::ApiError {
    todo!("implemented by api agent")
}
