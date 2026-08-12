//! Bounded, thread-safe ring buffer of finalized traces (Go tracex/store.go).
//! Never allocates beyond TRACE_BUFFER_SIZE and always returns deep copies so
//! callers cannot mutate stored state.

use std::sync::{Arc, Mutex};

use super::{Span, Trace, TRACE_BUFFER_SIZE};

/// Thread-safe ring buffer of finalized traces.
pub struct Store {
    inner: Mutex<StoreInner>,
}

struct StoreInner {
    buf: Vec<Option<Trace>>,
    /// Index of the oldest retained trace.
    start: usize,
    /// Number of retained traces (0..=TRACE_BUFFER_SIZE).
    count: usize,
}

impl Store {
    pub fn new() -> Arc<Store> {
        Arc::new(Store {
            inner: Mutex::new(StoreInner {
                buf: vec![None; TRACE_BUFFER_SIZE],
                start: 0,
                count: 0,
            }),
        })
    }

    /// Store a copy of t, evicting the oldest trace when full.
    pub fn add(&self, t: Trace) {
        let mut inner = self.inner.lock().expect("store mutex poisoned");
        let idx = inner.ring_idx(inner.start + inner.count);
        inner.buf[idx] = Some(t);
        if inner.count < TRACE_BUFFER_SIZE {
            inner.count += 1;
        } else {
            inner.start = inner.ring_idx(inner.start + 1);
        }
    }

    /// Number of retained traces.
    pub fn len(&self) -> usize {
        let inner = self.inner.lock().expect("store mutex poisoned");
        inner.count
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    /// Up to `limit` traces, newest first. limit clamped to [1, TRACE_BUFFER_SIZE].
    pub fn list(&self, limit: usize) -> Vec<Trace> {
        let limit = limit.clamp(1, TRACE_BUFFER_SIZE);
        let inner = self.inner.lock().expect("store mutex poisoned");
        let n = inner.count.min(limit);
        let mut out = Vec::with_capacity(n);
        for i in 0..n {
            let idx = inner.ring_idx(inner.start + inner.count - 1 - i); // newest first
            if let Some(t) = &inner.buf[idx] {
                out.push(t.clone());
            }
        }
        out
    }

    /// Deep copy of the trace with the given ID, if retained.
    pub fn get(&self, id: &str) -> Option<Trace> {
        if id.is_empty() {
            return None;
        }
        let inner = self.inner.lock().expect("store mutex poisoned");
        for i in 0..inner.count {
            let idx = inner.ring_idx(inner.start + i);
            if let Some(t) = &inner.buf[idx] {
                if t.trace_id == id {
                    return Some(t.clone());
                }
            }
        }
        None
    }

    /// Merge a late-arriving child span into an already-finalized trace.
    /// Returns false when the trace is not retained.
    pub fn append_span(&self, trace_id: &str, sp: Span) -> bool {
        if trace_id.is_empty() {
            return false;
        }
        let mut inner = self.inner.lock().expect("store mutex poisoned");
        for i in 0..inner.count {
            let idx = inner.ring_idx(inner.start + i);
            let t = match &mut inner.buf[idx] {
                Some(t) if t.trace_id == trace_id => t,
                _ => continue,
            };
            // Skip duplicates (retried exports or double-finalized spans).
            if t.spans.iter().any(|existing| existing.span_id == sp.span_id) {
                return true;
            }
            t.spans.push(sp);
            t.span_count = t.spans.len() as i32;
            if t.ended_at < t.spans.last().unwrap().ended_at {
                t.ended_at = t.spans.last().unwrap().ended_at;
                t.duration_ms = super::duration_ms(t.started_at, t.ended_at);
            }
            t.component_ids = component_ids(&t.spans);
            return true;
        }
        false
    }
}

impl StoreInner {
    /// Normalize an index into [0, TRACE_BUFFER_SIZE) without panicking on
    /// negative or overflow values (tiger: no unchecked arithmetic).
    fn ring_idx(&self, i: usize) -> usize {
        i % TRACE_BUFFER_SIZE
    }
}

/// Ordered unique set of traversed component node IDs, in span start order.
pub fn component_ids(spans: &[Span]) -> Vec<String> {
    let mut seen = std::collections::HashSet::new();
    let mut ids = Vec::new();
    for sp in spans {
        let c = sp.component.as_str();
        if c.is_empty() || c == "internal" || seen.contains(c) || !super::topology::is_node_id(c) {
            continue;
        }
        seen.insert(c.to_string());
        ids.push(c.to_string());
    }
    ids
}
