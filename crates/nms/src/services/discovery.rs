//! DiscoveryService: target expansion + plugin pool submission
//! (Go pkg/Services/discovery). Implemented by the services-discovery-health agent.

use std::collections::HashMap;
use std::sync::Arc;

use chrono::{DateTime, Utc};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::models::{DiscoveryProfile, Event};
use crate::plugin;
use crate::plugin_worker::{PluginRunner, PluginWorkerPool};
use crate::services::clock::Clock;

/// Max IPs a single target may expand to (a /16 block).
pub const MAX_EXPAND_HOSTS: usize = 1 << 16;
/// Max tasks per plugin invocation (winrm plugin cap: 10000).
pub const MAX_TASKS_PER_BATCH: usize = 10000;
/// How long a pending discovery may stay without a result before reaping.
pub const PENDING_STALE_AFTER_MIN: i64 = 30;

pub struct DiscoveryService {
    pub pool: PluginWorkerPool<plugin::Task, plugin::Result>,
    pub pool_results: mpsc::Receiver<Vec<plugin::Result>>,
    pub events: mpsc::Receiver<Event>,
    pub result_ch: mpsc::Sender<plugin::Result>,
    pub plugin_dir: String,
    pub encryption_key: String,
    pub pending: HashMap<String, (i64, i64, i32, DateTime<Utc>)>, // ip -> (disc_id, cred_id, port, created)
    pub clock: Arc<dyn Clock>,
}

impl DiscoveryService {
    pub fn new(
        events: mpsc::Receiver<Event>,
        result_ch: mpsc::Sender<plugin::Result>,
        plugin_dir: String,
        encryption_key: String,
        worker_count: i32,
        buffer_size: usize,
        runner: Arc<dyn PluginRunner>,
        clock: Arc<dyn Clock>,
    ) -> DiscoveryService {
        let (pool, pool_results) =
            PluginWorkerPool::new(worker_count as usize, "DiscoveryPool", buffer_size, runner);
        let svc = DiscoveryService {
            pool,
            pool_results,
            events,
            result_ch,
            plugin_dir,
            encryption_key,
            pending: HashMap::new(),
            clock,
        };
        svc
    }

    pub async fn start(&mut self, _ctx: CancellationToken) {
        todo!("implemented by services-discovery-health agent")
    }

    pub fn run_discovery(&mut self, _profile: &DiscoveryProfile) {
        todo!("implemented by services-discovery-health agent")
    }

    pub fn expand_target(target: &str) -> Result<Vec<String>, String> {
        let target = target.trim();
        if target.contains('/') {
            return Self::expand_cidr(target);
        }
        if target.contains('-') {
            return Self::expand_range(target);
        }
        if target.parse::<std::net::IpAddr>().is_ok() {
            return Ok(vec![target.to_string()]);
        }
        Err(format!("invalid target {target:?}"))
    }

    pub fn expand_cidr(_cidr: &str) -> Result<Vec<String>, String> {
        todo!("implemented by services-discovery-health agent")
    }

    pub fn expand_range(_range: &str) -> Result<Vec<String>, String> {
        todo!("implemented by services-discovery-health agent")
    }
}
