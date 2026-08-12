//! Contract between the core and external plugin binaries (Go pkg/plugin).
//! Plugins receive Tasks via stdin (JSON array) and return Results via stdout
//! (JSON array). The Credentials payload is protocol-specific and opaque to
//! the core — plugins parse it themselves. JSON shape is the wire contract.

use serde::{Deserialize, Serialize};

/// Input sent to a plugin binary.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Task {
    /// Optional: for tracking results back to a device.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub device_id: Option<i64>,
    /// IP address or hostname.
    pub target: String,
    /// Target port.
    pub port: i32,
    /// Decrypted JSON payload (protocol-specific).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub credentials: Option<serde_json::Value>,
    /// Span context of the submitting service.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub trace_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub span_id: Option<String>,
}

impl Task {
    pub fn span_context_ids(&self) -> (Option<String>, Option<String>) {
        (self.trace_id.clone(), self.span_id.clone())
    }
}

/// Output from a plugin binary.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Result {
    /// Echo back for correlation.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub device_id: Option<i64>,
    pub target: String,
    pub port: i32,
    pub success: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    /// Discovery mode.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub hostname: Option<String>,
    /// Polling mode (hierarchical raw data).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub data: Option<serde_json::Value>,
    /// Span context of the pool execution.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub trace_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub span_id: Option<String>,
    /// Provisioning context (set by discovery service; not on the wire).
    #[serde(skip)]
    pub discovery_profile_id: i64,
    #[serde(skip)]
    pub credential_profile_id: i64,
}

impl Result {
    pub fn set_span_context_ids(&mut self, trace_id: Option<String>, span_id: Option<String>) {
        self.trace_id = trace_id;
        self.span_id = span_id;
    }
}
