//! EntityService (caches, CRUD, provisioning) + MetricsService (worker pool)
//! (Go pkg/Services/persistence). Implemented by the services-persistence agent.

use std::sync::Arc;

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::database::{DbError, Repository};
use crate::models::{
    BatchDeviceResponse, CredentialProfile, Device, DiscoveryProfile, MetricResult, Request,
};
use crate::plugin;
use crate::services::clock::Clock;

pub struct EntityService {
    pub discovery_results: mpsc::Receiver<plugin::Result>,
    pub events_chan: mpsc::Receiver<crate::models::Event>,
    pub requests: mpsc::Receiver<Request>,
    pub credential_repo: Arc<dyn Repository<CredentialProfile>>,
    pub device_repo: Arc<dyn Repository<Device>>,
    pub discovery_profile_repo: Arc<dyn Repository<DiscoveryProfile>>,
    pub discovery_profile_events: mpsc::Sender<crate::models::Event>,
    pub device_events: mpsc::Sender<crate::models::Event>,
    pub clock: Arc<dyn Clock>,
}

impl EntityService {
    pub fn new(
        discovery_results: mpsc::Receiver<plugin::Result>,
        events_chan: mpsc::Receiver<crate::models::Event>,
        requests: mpsc::Receiver<Request>,
        credential_repo: Arc<dyn Repository<CredentialProfile>>,
        device_repo: Arc<dyn Repository<Device>>,
        discovery_profile_repo: Arc<dyn Repository<DiscoveryProfile>>,
        discovery_profile_events: mpsc::Sender<crate::models::Event>,
        device_events: mpsc::Sender<crate::models::Event>,
        clock: Arc<dyn Clock>,
    ) -> EntityService {
        EntityService {
            discovery_results,
            events_chan,
            requests,
            credential_repo,
            device_repo,
            discovery_profile_repo,
            discovery_profile_events,
            device_events,
            clock,
        }
    }

    pub async fn run(&mut self, _ctx: CancellationToken) {
        todo!("implemented by services-persistence agent")
    }
    pub async fn load_caches(&mut self) -> Result<(), String> {
        todo!("implemented by services-persistence agent")
    }
    pub fn get_active_device_ids(&self) -> Vec<i64> {
        todo!("implemented by services-persistence agent")
    }
    pub async fn handle_crud_request(&mut self, _req: Request) {
        todo!("implemented by services-persistence agent")
    }
    pub fn handle_get_batch(&self, _ids: Vec<i64>) -> BatchDeviceResponse {
        todo!("implemented by services-persistence agent")
    }
    pub fn handle_get_credential(&self, _id: i64) -> Option<Box<CredentialProfile>> {
        todo!("implemented by services-persistence agent")
    }
    pub async fn handle_deactivate_device(&mut self, _device_id: i64) -> Result<Box<Device>, String> {
        todo!("implemented by services-persistence agent")
    }
    pub fn device_cache_len(&self) -> usize {
        todo!("implemented by services-persistence agent")
    }
}

/// Metrics persistence boundary (DST: MemMetricsStore; prod: sqlx pools).
#[async_trait]
pub trait MetricsStore: Send + Sync {
    async fn insert_batch(
        &self,
        rows: Vec<(i64, serde_json::Value, DateTime<Utc>)>,
    ) -> Result<(), DbError>;
    async fn query(
        &self,
        device_id: i64,
        path: &str,
        start: DateTime<Utc>,
        end: DateTime<Utc>,
        limit: i32,
    ) -> Result<Vec<MetricResult>, DbError>;
}

pub struct MetricsService {
    pub poll_results: mpsc::Receiver<Vec<plugin::Result>>,
    pub query_reqs: mpsc::Receiver<Request>,
    pub write_store: Arc<dyn MetricsStore>,
    pub read_store: Arc<dyn MetricsStore>,
    pub worker_count: i32,
    pub failure_chan: mpsc::Sender<crate::models::Event>,
    pub default_limit: i32,
    pub default_range_hours: i32,
    pub clock: Arc<dyn Clock>,
}

impl MetricsService {
    pub fn new(
        poll_results: mpsc::Receiver<Vec<plugin::Result>>,
        query_reqs: mpsc::Receiver<Request>,
        write_store: Arc<dyn MetricsStore>,
        read_store: Arc<dyn MetricsStore>,
        worker_count: i32,
        failure_chan: mpsc::Sender<crate::models::Event>,
        default_limit: i32,
        default_range_hours: i32,
        clock: Arc<dyn Clock>,
    ) -> MetricsService {
        MetricsService {
            poll_results,
            query_reqs,
            write_store,
            read_store,
            worker_count,
            failure_chan,
            default_limit,
            default_range_hours,
            clock,
        }
    }

    pub async fn run(&mut self, _ctx: CancellationToken) {
        todo!("implemented by services-persistence agent")
    }
    pub fn validate_path(_path: &str) -> Result<(), String> {
        todo!("implemented by services-persistence agent")
    }
}
