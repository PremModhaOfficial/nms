//! Service layer (Go pkg/Services). Each service is a tokio task owning
//! bounded mpsc channels; time comes from an injected `Clock` (DST), plugin
//! execution and fping go through injectable traits (see plugin_worker and
//! scheduling). Constructor signatures below are the integration contract —
//! main.rs and the DST harness depend on them; do not change without updating
//! both.

pub mod clock;
pub mod discovery;
pub mod monitor_failure;
pub mod persistence;
pub mod polling;
pub mod scheduling;
