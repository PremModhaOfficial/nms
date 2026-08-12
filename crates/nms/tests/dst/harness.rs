//! DST harness: builds the full service topology with deterministic effects.
//!
//! Deterministic Simulation Testing = real service code + simulated clock
//! (ManualClock advanced in lockstep with tokio::time::advance) + scripted
//! effects (Pinger, PluginRunner, MemRepository, MemMetricsStore) + seeded
//! RNG. Every scenario is a numbered flow with exact per-step assertions.
//! No real sleeps, no real fping, no real plugin binaries, no real DB.

use std::collections::HashMap;
use std::sync::Arc;

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use nms::database::{DbError, MemRepository};
use nms::models::{CredentialProfile, Device, DiscoveryProfile, MetricResult};
use nms::plugin;
use nms::plugin_worker::{Pinger, PluginRunner};
use nms::services::clock::{Clock, ManualClock};
use nms::services::persistence::MetricsStore;
use nms::services::{
    discovery::DiscoveryService,
    monitor_failure::FailureService,
    persistence::{EntityService, MetricsService},
    polling::Poller,
    scheduling::Scheduler,
};
use nms::tracex;

/// Channel buffer sizes (must match lib.rs buffers / Go main.go).
pub const DATA_BUFFER_SIZE: usize = 1000;
pub const EVENT_BUFFER_SIZE: usize = 100;
pub const CONTROL_BUFFER_SIZE: usize = 50;
pub const TEST_ENCRYPTION_KEY: &str =
    "1234567890123456789012345678901212345678901234567890123456789012";

/// Fixed epoch so every scenario starts from the same instant.
pub fn epoch() -> DateTime<Utc> {
    DateTime::parse_from_rfc3339("2025-01-01T00:00:00Z")
        .expect("static RFC3339 parses")
        .with_timezone(&Utc)
}

/// Scripted pinger: reachability from a fixed map (absent IP = unreachable).
pub struct ScriptedPinger {
    pub reachable: HashMap<String, bool>,
}

#[async_trait]
impl Pinger for ScriptedPinger {
    async fn ping(&self, ips: &[String]) -> HashMap<String, bool> {
        ips.iter()
            .map(|ip| (ip.clone(), *self.reachable.get(ip).unwrap_or(&false)))
            .collect()
    }
}

/// Scripted plugin runner: echoes one success result per task.
#[derive(Clone, Default)]
pub struct ScriptedRunner;

#[async_trait]
impl PluginRunner for ScriptedRunner {
    async fn execute(
        &self,
        _bin_path: &str,
        _args: &[String],
        tasks: &[plugin::Task],
    ) -> Vec<plugin::Result> {
        tasks
            .iter()
            .map(|t| plugin::Result {
                device_id: t.device_id,
                target: t.target.clone(),
                port: t.port,
                success: true,
                error: None,
                hostname: None,
                data: Some(serde_json::json!({"cpu": {"total": 42}})),
                trace_id: t.trace_id.clone(),
                span_id: t.span_id.clone(),
                discovery_profile_id: 0,
                credential_profile_id: 0,
            })
            .collect()
    }
}

/// In-memory metrics store (DST stand-in for the sqlx metrics pools).
#[derive(Default)]
pub struct MemMetricsStore {
    pub rows: std::sync::Mutex<Vec<(i64, serde_json::Value, DateTime<Utc>)>>,
}

#[async_trait]
impl MetricsStore for MemMetricsStore {
    async fn insert_batch(
        &self,
        rows: Vec<(i64, serde_json::Value, DateTime<Utc>)>,
    ) -> Result<(), DbError> {
        self.rows.lock().expect("metrics store poisoned").extend(rows);
        Ok(())
    }

    async fn query(
        &self,
        device_id: i64,
        _path: &str,
        start: DateTime<Utc>,
        end: DateTime<Utc>,
        limit: i32,
    ) -> Result<Vec<MetricResult>, DbError> {
        let rows = self.rows.lock().expect("metrics store poisoned");
        let mut out: Vec<MetricResult> = rows
            .iter()
            .filter(|(id, _, ts)| *id == device_id && *ts >= start && *ts <= end)
            .map(|(_, v, ts)| MetricResult { timestamp: *ts, value: v.clone() })
            .collect();
        out.sort_by(|a, b| b.timestamp.cmp(&a.timestamp));
        out.truncate(limit.max(0) as usize);
        Ok(out)
    }

    fn row_count(&self) -> usize {
        self.rows.lock().expect("metrics store poisoned").len()
    }
}

/// Full in-process topology mirroring Go initServices, all effects scripted.
/// Tests inject via the *_tx senders and assert via repos/stores/state.
pub struct Topology {
    pub clock: Arc<ManualClock>,

    // Service handles (run loops started by the scenario).
    pub entity: EntityService,
    pub sched: Scheduler,
    pub poller: Poller,
    pub metrics: MetricsService,
    pub discovery: DiscoveryService,
    pub health: FailureService,

    // Repos + stores (assertion surface).
    pub cred_repo: Arc<MemRepository<CredentialProfile>>,
    pub dev_repo: Arc<MemRepository<Device>>,
    pub disc_repo: Arc<MemRepository<DiscoveryProfile>>,
    pub write_store: Arc<MemMetricsStore>,
    pub read_store: Arc<MemMetricsStore>,

    // Injection senders (Go main.go channel names).
    pub crud_tx: mpsc::Sender<nms::models::Request>,
    pub metric_tx: mpsc::Sender<nms::models::Request>,
    pub prov_tx: mpsc::Sender<nms::models::Event>,
    pub device_tx: mpsc::Sender<nms::models::Event>,
    pub disc_profile_tx: mpsc::Sender<nms::models::Event>,
    pub failure_tx: mpsc::Sender<nms::models::Event>,

    pub ctx: CancellationToken,
}

/// Build the topology exactly like Go initServices with scripted effects.
/// Service run loops are NOT started; scenarios spawn what they need.
pub fn build_topology(reachable: HashMap<String, bool>) -> Topology {
    let clock = Arc::new(ManualClock::new(epoch()));
    let _tracer = tracex::init_with_seed(clock.clone(), 42);

    // Channels (Go main.go names + buffer sizes).
    let (device_tx, device_rx) = mpsc::channel(EVENT_BUFFER_SIZE);
    let (disc_profile_tx, disc_profile_rx) = mpsc::channel(EVENT_BUFFER_SIZE);
    let (disc_result_tx, disc_result_rx) = mpsc::channel(EVENT_BUFFER_SIZE);
    let (poll_result_tx, poll_result_rx) = mpsc::channel(DATA_BUFFER_SIZE);
    let (sched_to_poll_tx, sched_to_poll_rx) = mpsc::channel(CONTROL_BUFFER_SIZE);
    let (failure_tx, failure_rx) = mpsc::channel(EVENT_BUFFER_SIZE);
    let (crud_tx, crud_rx) = mpsc::channel(EVENT_BUFFER_SIZE);
    let (metric_tx, metric_rx) = mpsc::channel(EVENT_BUFFER_SIZE);
    let (prov_tx, prov_rx) = mpsc::channel(EVENT_BUFFER_SIZE);

    // In-memory repos.
    let cred_repo = Arc::new(MemRepository::<CredentialProfile>::new());
    let dev_repo = Arc::new(MemRepository::<Device>::new());
    let disc_repo = Arc::new(MemRepository::<DiscoveryProfile>::new());

    let entity = EntityService::new(
        disc_result_rx,
        prov_rx,
        crud_rx,
        cred_repo.clone(),
        dev_repo.clone(),
        disc_repo.clone(),
        disc_profile_tx.clone(),
        device_tx.clone(),
        clock.clone(),
    );

    let sched = Scheduler::new(
        device_rx,
        crud_tx.clone(),
        sched_to_poll_tx,
        failure_tx.clone(),
        Arc::new(ScriptedPinger { reachable }),
        30,  // poll interval seconds
        500, // fping timeout ms
        2,   // fping retries
        clock.clone(),
    );

    let poller = Poller::new(
        "plugins".to_string(),
        TEST_ENCRYPTION_KEY.to_string(),
        5,
        DATA_BUFFER_SIZE,
        crud_tx.clone(),
        sched_to_poll_rx,
        poll_result_tx,
        Arc::new(ScriptedRunner),
        clock.clone(),
    );

    let write_store = Arc::new(MemMetricsStore::default());
    let read_store = Arc::new(MemMetricsStore::default());

    let metrics = MetricsService::new(
        poll_result_rx,
        metric_rx,
        write_store.clone(),
        read_store.clone(),
        4,
        failure_tx.clone(),
        100,
        1,
        clock.clone(),
    );

    let discovery = DiscoveryService::new(
        disc_profile_rx,
        disc_result_tx,
        "plugins".to_string(),
        TEST_ENCRYPTION_KEY.to_string(),
        3,
        EVENT_BUFFER_SIZE,
        Arc::new(ScriptedRunner),
        clock.clone(),
    );

    let health = FailureService::new(failure_rx, crud_tx.clone(), 3, 3, clock.clone());

    Topology {
        clock,
        entity,
        sched,
        poller,
        metrics,
        discovery,
        health,
        cred_repo,
        dev_repo,
        disc_repo,
        write_store,
        read_store,
        crud_tx,
        metric_tx,
        prov_tx,
        device_tx,
        disc_profile_tx,
        failure_tx,
        ctx: CancellationToken::new(),
    }
}

/// Advance the manual clock (call alongside tokio::time::advance in DST).
pub fn advance_clock(topo: &Topology, secs: u64) {
    topo.clock.advance(std::time::Duration::from_secs(secs));
}

/// Pump the runtime so queued messages make progress (deterministic under a
/// current-thread runtime: tasks run in spawn order, no randomness).
pub async fn pump() {
    for _ in 0..20 {
        tokio::task::yield_now().await;
    }
}

/// Advance simulated time AND the manual clock in lockstep.
pub async fn advance(topo: &Topology, secs: u64) {
    advance_clock(topo, secs);
    tokio::time::advance(std::time::Duration::from_secs(secs)).await;
    pump().await;
}

/// Build a device record with sensible defaults (id set by repo).
pub fn device(ip: &str, plugin_id: &str, should_ping: bool) -> Device {
    Device {
        id: 0,
        hostname: format!("host-{ip}"),
        ip_address: ip.to_string(),
        plugin_id: plugin_id.to_string(),
        port: 5986,
        credential_profile_id: 1,
        discovery_profile_id: 1,
        polling_interval_seconds: 60,
        should_ping,
        status: "active".to_string(),
        created_at: epoch(),
        updated_at: epoch(),
        credential_profile: None,
        discovery_profile: None,
    }
}
