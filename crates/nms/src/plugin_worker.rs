//! Generic plugin worker pool + injectable Pinger/PluginRunner (Go pkg/pluginWorker).
//! Implemented by the plugin_worker agent. Stub compiles; agent replaces bodies.

use std::collections::HashMap;
use std::sync::Arc;

use async_trait::async_trait;
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::plugin;

/// Ping reachability check (prod: fping binary; DST: scripted).
#[async_trait]
pub trait Pinger: Send + Sync {
    async fn ping(&self, ips: &[String]) -> HashMap<String, bool>;
}

/// Executes a plugin binary over a task batch (JSON stdin/stdout).
#[async_trait]
pub trait PluginRunner: Send + Sync {
    async fn execute(&self, bin_path: &str, args: &[String], tasks: &[plugin::Task]) -> Vec<plugin::Result>;
}

/// Production runner: spawn + 5-minute timeout + process-group kill.
pub struct ProcessPluginRunner;
impl ProcessPluginRunner {
    pub fn new() -> Arc<dyn PluginRunner> {
        todo!("implemented by plugin_worker agent")
    }
}

/// Production pinger: `fping -a -q -t <ms> -r <n> <ips...>`.
pub struct ProcessPinger;
impl ProcessPinger {
    pub fn new(_fping_path: String, _timeout_ms: i32, _retries: i32) -> Arc<dyn Pinger> {
        todo!("implemented by plugin_worker agent")
    }
}

/// Bounded worker pool executing plugin binaries with batched tasks.
pub struct PluginWorkerPool<T, R> {
    _marker: std::marker::PhantomData<(T, R)>,
}

impl<T, R> PluginWorkerPool<T, R>
where
    T: Send + 'static + serde::Serialize,
    R: Send + 'static + serde::de::DeserializeOwned,
{
    pub fn new(
        _worker_count: usize,
        _pool_name: &str,
        _buffer_size: usize,
        _runner: Arc<dyn PluginRunner>,
    ) -> (PluginWorkerPool<T, R>, mpsc::Receiver<Vec<R>>) {
        todo!("implemented by plugin_worker agent")
    }

    pub fn start(&mut self, _ctx: CancellationToken) {
        todo!("implemented by plugin_worker agent")
    }

    pub fn submit(&self, _bin_path: &str, _tasks: Vec<T>) -> bool {
        todo!("implemented by plugin_worker agent")
    }
}
