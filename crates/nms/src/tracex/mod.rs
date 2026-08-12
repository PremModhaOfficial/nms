//! In-process trace store + exporter for the dev dashboard (Go pkg/tracex).
//!
//! Ponytail: the Go code wires the OpenTelemetry SDK with a custom exporter;
//! we drop the SDK and keep the *contract* — the dashboard JSON shapes
//! (Trace/Span/SpanEvent), the ring-buffer Store, finalize-on-root-arrival,
//! late-span append, and MaskJSON. A lightweight custom tracer replaces OTel
//! so the whole pipeline is deterministic under DST (injectable clock + seed).

pub mod exporter;
pub mod mask;
pub mod store;
pub mod topology;

use std::collections::HashMap;
use std::sync::Arc;

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use rand::{Rng, SeedableRng};
use tokio::sync::Mutex;

use crate::services::clock::Clock;

/// Maximum traces retained by the ring buffer (Go TraceBufferSize).
pub const TRACE_BUFFER_SIZE: usize = 1000;
/// Max spans retained per trace; extras are dropped (Go maxSpansPerTrace).
pub const MAX_SPANS_PER_TRACE: usize = 500;
/// Max in-flight traces awaiting their root; oldest evicted (Go maxPendingTraces).
pub const MAX_PENDING_TRACES: usize = 2000;
/// Cap on any single string attribute / body (Go maxBodyAttrBytes = 64 KiB).
pub const MAX_BODY_ATTR_BYTES: usize = 64 << 10;

/// Finalized in-process trace as consumed by the dev dashboard. The JSON
/// shape is the frontend contract; do not rename fields without a coordinated
/// frontend change.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Trace {
    pub trace_id: String,
    pub root_span_id: String,
    pub started_at: DateTime<Utc>,
    pub ended_at: DateTime<Utc>,
    pub duration_ms: f64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub method: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub path: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub status_code: Option<i32>,
    pub component_ids: Vec<String>,
    pub span_count: i32,
    pub error: bool,
    pub spans: Vec<Span>,
}

/// A single span in the dashboard shape.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Span {
    pub span_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parent_id: Option<String>,
    pub name: String,
    pub kind: String,
    /// Topology node id: api, entity, scheduler, poller, pluginpool, metrics,
    /// db, discovery, discoverypool, health.
    pub component: String,
    pub started_at: DateTime<Utc>,
    pub ended_at: DateTime<Utc>,
    pub duration_ms: f64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub attributes: Option<HashMap<String, serde_json::Value>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub events: Option<Vec<SpanEvent>>,
}

/// A timed event recorded on a span (e.g. a request body).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SpanEvent {
    pub name: String,
    pub time: DateTime<Utc>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub attributes: Option<HashMap<String, serde_json::Value>>,
}

/// Live span context threaded explicitly through the call chain (the Rust
/// translation of Go's `context.Context` tracing state). Channel messages
/// carry `(trace_id, span_id)`; receivers rebuild a remote context from them.
#[derive(Debug, Clone, PartialEq)]
pub struct SpanCtx {
    pub trace_id: String,
    pub span_id: String,
    pub parent_id: Option<String>,
}

impl SpanCtx {
    /// Hex ids for stamping outgoing messages.
    pub fn ids(&self) -> (Option<String>, Option<String>) {
        (Some(self.trace_id.clone()), Some(self.span_id.clone()))
    }
}

/// A running span; ends (records end time and hands off to the exporter) on
/// drop. Attributes/events must be recorded before the guard is dropped.
pub struct SpanGuard {
    ctx: SpanCtx,
    name: String,
    kind: String,
    component: String,
    is_root: bool,
    started_at: DateTime<Utc>,
    ended_at: Option<DateTime<Utc>>,
    attributes: HashMap<String, serde_json::Value>,
    events: Vec<SpanEvent>,
    clock: Arc<dyn Clock>,
    exporter: Arc<exporter::Exporter>,
}

impl SpanGuard {
    /// Record an attribute on the span (Go span.SetAttributes).
    pub fn attr(&mut self, key: impl Into<String>, value: impl Into<serde_json::Value>) {
        self.attributes.insert(key.into(), value.into());
    }

    /// Record a timed event (Go span.AddEvent).
    pub fn event(&mut self, name: impl Into<String>, attrs: Vec<(String, serde_json::Value)>) {
        self.events.push(SpanEvent {
            name: name.into(),
            time: self.clock.now(),
            attributes: if attrs.is_empty() { None } else { Some(attrs.into_iter().collect()) },
        });
    }

    /// Mark the span as errored (Go span.SetStatus(Error)).
    pub fn set_error(&mut self) {
        self.attributes.insert("nms.trace.error".into(), serde_json::Value::Bool(true));
    }

    pub fn ctx(&self) -> &SpanCtx {
        &self.ctx
    }
}

impl Drop for SpanGuard {
    fn drop(&mut self) {
        let now = self.clock.now();
        self.ended_at = Some(now);
        let span = Span {
            span_id: self.ctx.span_id.clone(),
            parent_id: self.ctx.parent_id.clone(),
            name: self.name.clone(),
            kind: self.kind.clone(),
            component: self.component.clone(),
            started_at: self.started_at,
            ended_at: self.ended_at.unwrap_or(now),
            duration_ms: duration_ms(self.started_at, now),
            attributes: if self.attributes.is_empty() { None } else { Some(std::mem::take(&mut self.attributes)) },
            events: if self.events.is_empty() { None } else { Some(std::mem::take(&mut self.events)) },
        };
        self.exporter.add_span(self.ctx.clone(), span, self.is_root);
    }
}

/// Process-wide tracer backed by the exporter + store. One per process;
/// tests build their own with a ManualClock + seed for DST.
pub struct Tracer {
    clock: Arc<dyn Clock>,
    rng: Mutex<rand::rngs::StdRng>,
    exporter: Arc<exporter::Exporter>,
}

impl Tracer {
    /// Build a tracer with an injectable clock and a seeded RNG (DST).
    pub fn new(clock: Arc<dyn Clock>, seed: u64) -> Arc<Tracer> {
        let store = store::Store::new();
        let exporter = Arc::new(exporter::Exporter::new(store));
        Arc::new(Tracer {
            clock,
            rng: Mutex::new(rand::rngs::StdRng::seed_from_u64(seed)),
            exporter,
        })
    }

    pub fn store(&self) -> Arc<store::Store> {
        self.exporter.store()
    }

    fn gen_hex(&self, bytes: usize) -> String {
        let mut buf = vec![0u8; bytes];
        let mut rng = self.rng.blocking_lock();
        rng.fill_bytes(&mut buf);
        hex::encode(buf)
    }

    /// Start a span. `parent` is the calling span's ctx (or a remote context
    /// rebuilt from a channel message's ids). `is_root` marks HTTP root spans
    /// (nms.root=true) so the exporter finalizes the trace on their arrival.
    pub fn start(
        &self,
        component: &str,
        name: &str,
        kind: &str,
        parent: Option<&SpanCtx>,
        is_root: bool,
    ) -> (SpanCtx, SpanGuard) {
        let (trace_id, parent_id) = match parent {
            Some(p) => (p.trace_id.clone(), Some(p.span_id.clone())),
            None => (self.gen_hex(16), None),
        };
        let span_id = self.gen_hex(8);
        let ctx = SpanCtx { trace_id, span_id, parent_id };
        let guard = SpanGuard {
            ctx: ctx.clone(),
            name: name.to_string(),
            kind: kind.to_string(),
            component: component.to_string(),
            is_root,
            started_at: self.clock.now(),
            ended_at: None,
            attributes: HashMap::new(),
            events: Vec::new(),
            clock: self.clock.clone(),
            exporter: self.exporter.clone(),
        };
        (ctx, guard)
    }

    /// Rebuild a remote SpanCtx from channel-carried ids, or None when absent.
    pub fn remote_context(&self, trace_id: Option<&str>, span_id: Option<&str>) -> Option<SpanCtx> {
        let (tid, sid) = (trace_id?, span_id?);
        if tid.len() != 32 || sid.len() != 16 {
            return None;
        }
        Some(SpanCtx { trace_id: tid.to_string(), span_id: sid.to_string(), parent_id: None })
    }
}

fn duration_ms(start: DateTime<Utc>, end: DateTime<Utc>) -> f64 {
    (end - start).num_microseconds().unwrap_or(0) as f64 / 1000.0
}

/// Global tracer initialized by main (and by tests via init_with_seed).
static GLOBAL: std::sync::OnceLock<Arc<Tracer>> = std::sync::OnceLock::new();

/// Initialize the process-wide tracer (idempotent). Prod path.
pub fn init(clock: Arc<dyn Clock>) -> Arc<Tracer> {
    init_with_seed(clock, 0)
}

/// Initialize with an explicit RNG seed for deterministic tests.
pub fn init_with_seed(clock: Arc<dyn Clock>, seed: u64) -> Arc<Tracer> {
    GLOBAL.get_or_init(|| Tracer::new(clock, seed)).clone()
}

/// The process-wide tracer, or a fresh one when uninitialized (tests).
pub fn default() -> Arc<Tracer> {
    GLOBAL.get_or_init(|| Tracer::new(Arc::new(crate::services::clock::SystemClock), 0)).clone()
}
