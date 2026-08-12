//! Domain models: entities, events, request/reply protocol, and the bounded
//! RPC helper. Ported from Go `pkg/models` with enums replacing `interface{}`
//! payloads and `oneshot` replacing reply channels (tiger: typed, exhaustive).

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use sqlx::FromRow;
use tokio::sync::oneshot;
use tokio::time::Duration;

/// Bounds every synchronous request-reply exchange (Go: RPCTimeout).
pub const RPC_TIMEOUT: Duration = Duration::from_secs(10);

// ─────────────────────────────────────────────────────────────────────────────
// ENTITIES
// ─────────────────────────────────────────────────────────────────────────────

/// TableNamer supplies the SQL table name for an entity. Rust translation of
/// Go's `TableName()` method (tiger: compile-time, cannot be stale).
pub trait TableNamer {
    const TABLE: &'static str;
}

/// credential_profiles table.
#[derive(Debug, Clone, Serialize, Deserialize, FromRow, PartialEq)]
pub struct CredentialProfile {
    pub id: i64,
    pub name: String,
    pub protocol: String,
    /// Encrypted credential data (hex, AES-256-GCM). On update, "" or
    /// "[HIDDEN]" means "preserve existing ciphertext" (update omitempty).
    pub payload: String,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

impl TableNamer for CredentialProfile {
    const TABLE: &'static str = "credential_profiles";
}

/// discovery_profiles table.
#[derive(Debug, Clone, Serialize, Deserialize, FromRow, PartialEq)]
pub struct DiscoveryProfile {
    pub id: i64,
    pub name: String,
    /// CIDR, IP range, or single IP.
    pub target: String,
    pub port: i32,
    pub credential_profile_id: i64,
    pub auto_provision: bool,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
    /// Populated by cache lookup, not DB join (Go: `db:"-"`).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub credential_profile: Option<Box<CredentialProfile>>,
}

impl TableNamer for DiscoveryProfile {
    const TABLE: &'static str = "discovery_profiles";
}

/// devices table. credential_profile_id and discovery_profile_id are
/// immutable after creation (validated in EntityService).
#[derive(Debug, Clone, Serialize, Deserialize, FromRow, PartialEq)]
pub struct Device {
    pub id: i64,
    pub hostname: String,
    pub ip_address: String,
    pub plugin_id: String,
    pub port: i32,
    pub credential_profile_id: i64,
    pub discovery_profile_id: i64,
    pub polling_interval_seconds: i32,
    pub should_ping: bool,
    pub status: String, // "discovered" | "active" | "inactive" | "error"
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
    /// Populated by cache lookup, not DB join.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub credential_profile: Option<Box<CredentialProfile>>,
    /// Populated by cache lookup, not DB join.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub discovery_profile: Option<Box<DiscoveryProfile>>,
}

impl TableNamer for Device {
    const TABLE: &'static str = "devices";
}

/// Request for metric data (JSON path over the metrics JSONB column).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct MetricQuery {
    pub path: String,
    pub start: Option<DateTime<Utc>>,
    pub end: Option<DateTime<Utc>>,
    pub limit: i32,
}

/// Batch metric query payload (Go: persistence.MetricQueryRequest). Lives here
/// so api and services share one definition (Rust has no interface{}).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct MetricQueryRequest {
    pub device_ids: Vec<i64>,
    pub query: MetricQuery,
}

/// One point in a metrics time series (Go: persistence.MetricResult).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct MetricResult {
    pub timestamp: DateTime<Utc>,
    pub value: serde_json::Value,
}

/// Metrics results grouped by device (Go: persistence.BatchMetricResult).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct BatchMetricResult {
    pub device_id: i64,
    pub results: Vec<MetricResult>,
}

// ─────────────────────────────────────────────────────────────────────────────
// OPERATIONS & ENTITY TYPES
// ─────────────────────────────────────────────────────────────────────────────

/// Request-reply operations (Go string constants). Typed enum: exhaustive
/// match at every dispatch site (tiger: every branch has an else).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Operation {
    List,
    Get,
    Create,
    Update,
    Delete,
    /// Metrics query.
    Query,
    /// Batch lookup by IDs; returns devices split by should_ping.
    GetBatch,
    /// Get credential by profile ID.
    GetCredential,
    /// Deactivate a device (set status to inactive).
    DeactivateDevice,
}

impl Operation {
    pub fn as_str(self) -> &'static str {
        match self {
            Operation::List => "list",
            Operation::Get => "get",
            Operation::Create => "create",
            Operation::Update => "update",
            Operation::Delete => "delete",
            Operation::Query => "query",
            Operation::GetBatch => "get_batch",
            Operation::GetCredential => "get_credential",
            Operation::DeactivateDevice => "deactivate_device",
        }
    }
}

/// Entity type selector for CRUD routing (Go string "Device" etc.).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum EntityType {
    CredentialProfile,
    Device,
    DiscoveryProfile,
    Metric,
}

impl EntityType {
    pub fn as_str(self) -> &'static str {
        match self {
            EntityType::CredentialProfile => "CredentialProfile",
            EntityType::Device => "Device",
            EntityType::DiscoveryProfile => "DiscoveryProfile",
            EntityType::Metric => "Metric",
        }
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// EVENTS
// ─────────────────────────────────────────────────────────────────────────────

/// CRUD / command event type.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum EventType {
    Create,
    Update,
    Delete,
    /// Command: trigger discovery for a profile.
    TriggerDiscovery,
    /// Command: provision a discovered device.
    ProvisionDevice,
    /// Ping or poll failure.
    DeviceFailure,
    /// Explicitly run discovery for a profile.
    RunDiscovery,
}

impl EventType {
    pub fn as_str(self) -> &'static str {
        match self {
            EventType::Create => "create",
            EventType::Update => "update",
            EventType::Delete => "delete",
            EventType::TriggerDiscovery => "trigger_discovery",
            EventType::ProvisionDevice => "provision_device",
            EventType::DeviceFailure => "device_failure",
            EventType::RunDiscovery => "run_discovery",
        }
    }
}

/// Typed payload of an Event (replaces Go `interface{}`).
#[derive(Debug, Clone)]
pub enum EventPayload {
    Device(Box<Device>),
    DiscoveryProfile(Box<DiscoveryProfile>),
    DiscoveryTrigger(DiscoveryTriggerEvent),
    DeviceProvision(DeviceProvisionEvent),
    DeviceFailure(DeviceFailureEvent),
}

/// Event for scheduler cache synchronization / service commands.
#[derive(Debug, Clone)]
pub struct Event {
    pub event_type: EventType,
    pub payload: EventPayload,
    /// Span context of the sender, so the receiver continues the trace.
    pub trace_id: Option<String>,
    pub span_id: Option<String>,
}

impl Event {
    pub fn new(event_type: EventType, payload: EventPayload) -> Self {
        Event {
            event_type,
            payload,
            trace_id: None,
            span_id: None,
        }
    }
}

/// Command to trigger discovery (Go: DiscoveryTriggerEvent).
#[derive(Debug, Clone, PartialEq)]
pub struct DiscoveryTriggerEvent {
    pub discovery_profile_id: i64,
}

/// Command to provision a discovered device (Go: DeviceProvisionEvent).
#[derive(Debug, Clone, PartialEq)]
pub struct DeviceProvisionEvent {
    pub device_id: i64,
    pub polling_interval_seconds: i32,
}

/// Device failure from ping or poll (Go: DeviceFailureEvent).
#[derive(Debug, Clone, PartialEq)]
pub struct DeviceFailureEvent {
    pub device_id: i64,
    pub timestamp: DateTime<Utc>,
    /// "ping" or "poll".
    pub reason: String,
}

// ─────────────────────────────────────────────────────────────────────────────
// REQUEST / RESPONSE
// ─────────────────────────────────────────────────────────────────────────────

/// Typed payload of a Request (replaces Go `interface{}`).
#[derive(Debug, Clone)]
pub enum RequestPayload {
    CredentialProfile(Box<CredentialProfile>),
    Device(Box<Device>),
    DiscoveryProfile(Box<DiscoveryProfile>),
    MetricQuery(MetricQueryRequest),
    None,
}

/// Typed data of a Response (replaces Go `interface{}`).
#[derive(Debug, Clone)]
pub enum ResponseData {
    CredentialProfile(Box<CredentialProfile>),
    Device(Box<Device>),
    DiscoveryProfile(Box<DiscoveryProfile>),
    CredentialList(Vec<CredentialProfile>),
    DeviceList(Vec<Device>),
    DiscoveryProfileList(Vec<DiscoveryProfile>),
    BatchDevices(BatchDeviceResponse),
    MetricResults(Vec<BatchMetricResult>),
    /// Generic success marker (Go `map[string]any{"message": ...}` handled at api).
    Message(String),
    None,
}

/// Response to a Request. `error` is an Option<ApiError>; see api::ApiError
/// for the classified error type shared across the service boundary.
#[derive(Debug)]
pub struct Response {
    pub data: ResponseData,
    pub error: Option<Box<dyn std::error::Error + Send + Sync>>,
}

impl Response {
    pub fn ok(data: ResponseData) -> Self {
        Response { data, error: None }
    }

    pub fn err<E: std::error::Error + Send + Sync + 'static>(error: E) -> Self {
        Response {
            data: ResponseData::None,
            error: Some(Box::new(error)),
        }
    }
}

/// Payload of OpGetBatch responses: devices split by should_ping flag.
#[derive(Debug, Clone, PartialEq)]
pub struct BatchDeviceResponse {
    pub to_ping: Vec<Box<Device>>,
    pub to_skip: Vec<Box<Device>>,
}

/// Point-to-point message with a oneshot reply channel for synchronous
/// communication (Go: chan Response; Rust: oneshot Sender).
pub struct Request {
    pub operation: Operation,
    pub entity_type: EntityType,
    pub id: Option<i64>,
    pub ids: Vec<i64>,
    pub payload: RequestPayload,
    pub reply: oneshot::Sender<Response>,
    /// Span context of the sender, so the receiver continues the trace.
    pub trace_id: Option<String>,
    pub span_id: Option<String>,
}

impl Request {
    /// Build a request with a fresh reply channel. Returns (request, receiver).
    pub fn new(
        operation: Operation,
        entity_type: EntityType,
        id: Option<i64>,
        payload: RequestPayload,
    ) -> (Request, oneshot::Receiver<Response>) {
        let (reply, rx) = oneshot::channel();
        (
            Request {
                operation,
                entity_type,
                id,
                ids: Vec::new(),
                payload,
                reply,
                trace_id: None,
                span_id: None,
            },
            rx,
        )
    }

    /// Batch variant (OpGetBatch).
    pub fn with_ids(
        operation: Operation,
        entity_type: EntityType,
        ids: Vec<i64>,
    ) -> (Request, oneshot::Receiver<Response>) {
        let (reply, rx) = oneshot::channel();
        (
            Request {
                operation,
                entity_type,
                id: None,
                ids,
                payload: RequestPayload::None,
                reply,
                trace_id: None,
                span_id: None,
            },
            rx,
        )
    }
}

/// RPC error from the Call helper.
#[derive(Debug, thiserror::Error)]
pub enum CallError {
    #[error("rpc timeout")]
    Timeout,
    #[error("request channel closed")]
    ChannelClosed,
    #[error("reply channel closed")]
    ReplyClosed,
}

/// Call sends req to req_ch and waits for the reply on the oneshot receiver,
/// bounded by `timeout` (api handlers: 5s; services: RPC_TIMEOUT). Returns
/// the reply or a CallError instead of hanging forever (tiger: bounded wait).
pub async fn call(
    req_ch: &tokio::sync::mpsc::Sender<Request>,
    req: Request,
    rx: oneshot::Receiver<Response>,
    timeout: Duration,
) -> Result<Response, CallError> {
    if req_ch.send(req).await.is_err() {
        return Err(CallError::ChannelClosed);
    }
    match tokio::time::timeout(timeout, rx).await {
        Ok(Ok(resp)) => Ok(resp),
        Ok(Err(_)) => Err(CallError::ReplyClosed),
        Err(_) => Err(CallError::Timeout),
    }
}
