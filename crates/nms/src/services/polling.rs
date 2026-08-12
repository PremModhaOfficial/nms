//! Poller: groups devices by plugin, fetches credentials, submits to pool
//! (Go pkg/Services/polling). Implemented by the services-core agent.

use std::collections::HashMap;
use std::sync::Arc;

use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::models::{CredentialProfile, Device, Request};
use crate::plugin;
use crate::plugin_worker::{PluginRunner, PluginWorkerPool};
use crate::services::clock::Clock;

pub struct Poller {
    pub pool: PluginWorkerPool<plugin::Task, plugin::Result>,
    pub pool_results: mpsc::Receiver<Vec<plugin::Result>>,
    pub plugin_dir: String,
    pub plugins: HashMap<String, String>,
    pub encryption_key: String,
    pub entity_req_chan: mpsc::Sender<Request>,
    pub input_chan: mpsc::Receiver<Vec<Box<Device>>>,
    pub output_chan: mpsc::Sender<Vec<plugin::Result>>,
    pub clock: Arc<dyn Clock>,
}

impl Poller {
    pub fn new(
        plugin_dir: String,
        encryption_key: String,
        worker_count: i32,
        buffer_size: usize,
        entity_req_chan: mpsc::Sender<Request>,
        input_chan: mpsc::Receiver<Vec<Box<Device>>>,
        output_chan: mpsc::Sender<Vec<plugin::Result>>,
        runner: Arc<dyn PluginRunner>,
        clock: Arc<dyn Clock>,
    ) -> Poller {
        let (pool, pool_results) =
            PluginWorkerPool::new(worker_count as usize, "PollPool", buffer_size, runner);
        let mut p = Poller {
            pool,
            pool_results,
            plugin_dir,
            plugins: HashMap::new(),
            encryption_key,
            entity_req_chan,
            input_chan,
            output_chan,
            clock,
        };
        p.load_plugins();
        p
    }

    pub fn load_plugins(&mut self) {
        todo!("implemented by services-core agent")
    }

    pub async fn run(&mut self, _ctx: CancellationToken) {
        todo!("implemented by services-core agent")
    }

    pub fn group_by_protocol(&self, _devices: &[Box<Device>]) -> HashMap<String, Vec<Box<Device>>> {
        todo!("implemented by services-core agent")
    }

    pub async fn get_credential(
        &self,
        _ctx: &CancellationToken,
        _profile_id: i64,
    ) -> Option<Box<CredentialProfile>> {
        todo!("implemented by services-core agent")
    }
}
