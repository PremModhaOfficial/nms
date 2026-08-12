//! Injectable clock — the foundation of Deterministic Simulation Testing.
//! Services never call `Utc::now()` directly; they ask `Arc<dyn Clock>` so
//! tests can substitute a `ManualClock` advanced in lockstep with
//! `tokio::time::advance`.

use std::sync::{Arc, Mutex};
use std::time::Duration;

use chrono::{DateTime, Utc};

/// Source of wall-clock time for services.
pub trait Clock: Send + Sync {
    fn now(&self) -> DateTime<Utc>;
}

/// Real wall clock (production).
pub struct SystemClock;

impl Clock for SystemClock {
    fn now(&self) -> DateTime<Utc> {
        Utc::now()
    }
}

/// Deterministic clock for DST: the test advances it explicitly.
#[derive(Clone, Default)]
pub struct ManualClock {
    inner: Arc<Mutex<DateTime<Utc>>>,
}

impl ManualClock {
    pub fn new(start: DateTime<Utc>) -> Self {
        ManualClock { inner: Arc::new(Mutex::new(start)) }
    }

    /// Advance the simulated clock (call alongside tokio::time::advance in DST).
    pub fn advance(&self, d: Duration) {
        let mut now = self.inner.lock().expect("manual clock poisoned");
        *now = *now + chrono::Duration::from_std(d).expect("duration fits chrono");
    }

    pub fn set(&self, now: DateTime<Utc>) {
        let mut cur = self.inner.lock().expect("manual clock poisoned");
        *cur = now;
    }
}

impl Clock for ManualClock {
    fn now(&self) -> DateTime<Utc> {
        *self.inner.lock().expect("manual clock poisoned")
    }
}
