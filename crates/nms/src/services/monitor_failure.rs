//! FailureService: sliding-window failure tracking + deactivation
//! (Go pkg/Services/monitorFailure). Implemented by the services-discovery-health agent.

use std::collections::HashMap;
use std::sync::Arc;

use chrono::{DateTime, Utc};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::models::{DeviceFailureEvent, Event, Request};
use crate::services::clock::Clock;

/// Failure state for a single device.
#[derive(Debug, Clone, PartialEq)]
pub struct FailureRecord {
    pub last_time: DateTime<Utc>,
    pub count: i32,
}

pub struct FailureService {
    pub failures: HashMap<i64, FailureRecord>,
    pub failure_chan: mpsc::Receiver<Event>,
    pub entity_req_chan: mpsc::Sender<Request>,
    pub window_min: i32,
    pub threshold: i32,
    pub clock: Arc<dyn Clock>,
}

impl FailureService {
    pub fn new(
        failure_chan: mpsc::Receiver<Event>,
        entity_req_chan: mpsc::Sender<Request>,
        window_min: i32,
        threshold: i32,
        clock: Arc<dyn Clock>,
    ) -> FailureService {
        FailureService {
            failures: HashMap::new(),
            failure_chan,
            entity_req_chan,
            window_min,
            threshold,
            clock,
        }
    }

    pub async fn run(&mut self, _ctx: CancellationToken) {
        todo!("implemented by services-discovery-health agent")
    }

    /// Process one failure; returns Some(device_id) when the threshold is hit
    /// (caller deactivates). Mirrors Go handleFailure.
    pub fn handle_failure(&mut self, event: &DeviceFailureEvent) -> Option<i64> {
        let window = chrono::Duration::minutes(self.window_min as i64);
        let record = self.failures.entry(event.device_id).or_insert(FailureRecord {
            last_time: event.timestamp,
            count: 0,
        });
        if event.timestamp.signed_duration_since(record.last_time) < window {
            record.count += 1;
            if record.count >= self.threshold {
                self.failures.remove(&event.device_id);
                return Some(event.device_id);
            }
        } else {
            record.count = 1;
        }
        record.last_time = event.timestamp;
        None
    }

    pub fn failure_count(&self, device_id: i64) -> i32 {
        self.failures.get(&device_id).map(|r| r.count).unwrap_or(0)
    }
}
