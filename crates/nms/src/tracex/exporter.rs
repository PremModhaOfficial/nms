//! Groups spans by trace ID and finalizes a trace when its root span arrives
//! (Go tracex/exporter.go). Children always end before the parent, so root
//! arrival means the trace is complete. Late child spans (async channel
//! continuations) are appended to already-finalized traces.
//!
//! tiger: the exporter lock is never held while the store lock is taken —
//! finalize returns the Trace and add_span stores it after unlocking.

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use super::store::Store;
use super::{Span, SpanCtx, Trace, MAX_PENDING_TRACES, MAX_SPANS_PER_TRACE};

/// Groups spans by trace ID in a pending map; finalizes traces on root arrival.
pub struct Exporter {
    store: Arc<Store>,
    inner: Mutex<ExporterInner>,
}

struct ExporterInner {
    pending: HashMap<String, Vec<Span>>,
    /// Insertion order of pending trace IDs, for FIFO eviction.
    order: Vec<String>,
}

impl Exporter {
    pub fn new(store: Arc<Store>) -> Exporter {
        Exporter {
            store,
            inner: Mutex::new(ExporterInner { pending: HashMap::new(), order: Vec::new() }),
        }
    }

    pub fn store(&self) -> Arc<Store> {
        self.store.clone()
    }

    /// Ingest an ended span (called by SpanGuard::drop). Root spans finalize
    /// their trace; child spans pend until the root arrives.
    pub fn add_span(&self, ctx: SpanCtx, span: Span, is_root: bool) {
        let mut inner = self.inner.lock().expect("exporter mutex poisoned");
        let trace_id = ctx.trace_id.clone();

        if is_root {
            let mut spans = inner.pending.remove(&trace_id).unwrap_or_default();
            inner.order.retain(|id| id != &trace_id);
            spans.push(span);
            let trace = finalize_locked(&mut inner, trace_id, spans);
            drop(inner); // release before store call (no nested locks)
            self.store.add(trace);
            return;
        }

        // Late child span: root already ended and the trace was finalized.
        if self.store.get(&trace_id).is_some() {
            drop(inner); // release before store call (no nested locks)
            self.store.append_span(&trace_id, span);
            return;
        }

        if !inner.pending.contains_key(&trace_id) {
            inner.order.push(trace_id.clone());
        }
        inner.pending.entry(trace_id).or_default().push(span);
        evict_locked(&mut inner);
    }
}

/// Convert a completed span set into a Trace. The earliest-starting span is
/// the root (roots begin before any child). Never touches the store.
fn finalize_locked(inner: &mut ExporterInner, trace_id: String, mut spans: Vec<Span>) -> Trace {
    assert!(!spans.is_empty(), "finalize called with no spans");
    spans.sort_by(|a, b| {
        a.started_at
            .cmp(&b.started_at)
            .then_with(|| a.span_id.cmp(&b.span_id))
    });
    // tiger: bound kept — extras dropped, earliest kept.
    if spans.len() > MAX_SPANS_PER_TRACE {
        spans.truncate(MAX_SPANS_PER_TRACE);
    }
    let root = spans[0].clone();
    let mut t = Trace {
        trace_id,
        root_span_id: root.span_id.clone(),
        started_at: root.started_at,
        ended_at: root.ended_at,
        duration_ms: root.duration_ms,
        method: None,
        path: None,
        status_code: None,
        component_ids: Vec::new(),
        span_count: spans.len() as i32,
        error: false,
        spans,
    };
    extract_root_fields(&root, &mut t);
    t.component_ids = super::store::component_ids(&t.spans);
    let _ = inner; // pending bookkeeping already done by caller
    t
}

/// Drop the oldest pending traces until within MAX_PENDING_TRACES.
fn evict_locked(inner: &mut ExporterInner) {
    while inner.pending.len() > MAX_PENDING_TRACES && !inner.order.is_empty() {
        let id = inner.order.remove(0);
        inner.pending.remove(&id);
    }
}

/// Pull request-level metadata off the root span's attributes.
fn extract_root_fields(root: &Span, t: &mut Trace) {
    let attrs = root.attributes.as_ref();
    if let Some(attrs) = attrs {
        if let Some(v) = attrs.get("http.method").and_then(|v| v.as_str()) {
            t.method = Some(v.to_string());
        }
        if let Some(v) = attrs.get("http.route").and_then(|v| v.as_str()) {
            t.path = Some(v.to_string());
        }
        if let Some(v) = attrs.get("http.status_code").and_then(|v| v.as_i64()) {
            t.status_code = Some(v as i32);
        }
        if let Some(v) = attrs.get("nms.trace.error").and_then(|v| v.as_bool()) {
            t.error = v;
        }
    }
}
