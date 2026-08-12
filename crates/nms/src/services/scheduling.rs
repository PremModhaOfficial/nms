//! Scheduler: deadline min-heap + fping qualification (Go pkg/Services/scheduling).
//! Implemented by the services-core agent. Stub compiles; agent replaces bodies.

use std::cmp::Ordering;
use std::collections::BinaryHeap;
use std::sync::Arc;

use chrono::{DateTime, Utc};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::models::{Device, Event, Request};
use crate::plugin_worker::Pinger;
use crate::services::clock::Clock;

/// Lightweight priority-queue entry (min-heap by deadline).
#[derive(Debug, Clone)]
pub struct DeviceDeadline {
    pub device_id: i64,
    pub deadline: DateTime<Utc>,
}

impl PartialEq for DeviceDeadline {
    fn eq(&self, other: &Self) -> bool {
        self.deadline == other.deadline && self.device_id == other.device_id
    }
}
impl Eq for DeviceDeadline {}
impl PartialOrd for DeviceDeadline {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}
impl Ord for DeviceDeadline {
    fn cmp(&self, other: &Self) -> Ordering {
        // Reverse so BinaryHeap (max-heap) acts as a min-heap on deadline.
        other.deadline.cmp(&self.deadline).then_with(|| other.device_id.cmp(&self.device_id))
    }
}

/// Min-heap of device deadlines.
#[derive(Default)]
pub struct DeadlineQueue {
    heap: BinaryHeap<DeviceDeadline>,
}

impl DeadlineQueue {
    pub fn new() -> Self {
        DeadlineQueue { heap: BinaryHeap::new() }
    }
    pub fn len(&self) -> usize {
        self.heap.len()
    }
    pub fn is_empty(&self) -> bool {
        self.heap.is_empty()
    }
    pub fn push_entry(&mut self, device_id: i64, deadline: DateTime<Utc>) {
        self.heap.push(DeviceDeadline { device_id, deadline });
    }
    pub fn init_queue(&mut self, device_ids: Vec<i64>, now: DateTime<Utc>) {
        self.heap.clear();
        for id in device_ids {
            self.heap.push(DeviceDeadline { device_id: id, deadline: now });
        }
    }
    pub fn pop_expired(&mut self, now: DateTime<Utc>) -> Vec<DeviceDeadline> {
        let mut out = Vec::new();
        while let Some(entry) = self.heap.peek() {
            if entry.deadline > now {
                break;
            }
            out.push(self.heap.pop().expect("peeked entry must pop"));
        }
        out
    }
    pub fn push_batch(&mut self, entries: Vec<DeviceDeadline>) {
        for e in entries {
            self.heap.push(e);
        }
    }
}

/// Schedules devices for polling based on deadlines.
pub struct Scheduler {
    pub queue: DeadlineQueue,
    pub entity_req_chan: mpsc::Sender<Request>,
    pub device_events: mpsc::Receiver<Event>,
    pub output_chan: mpsc::Sender<Vec<Box<Device>>>,
    pub failure_chan: mpsc::Sender<Event>,
    pub pinger: Arc<dyn Pinger>,
    pub poll_interval_sec: i32,
    pub fping_timeout_ms: i32,
    pub fping_retries: i32,
    pub clock: Arc<dyn Clock>,
}

impl Scheduler {
    pub fn new(
        device_events: mpsc::Receiver<Event>,
        entity_req_chan: mpsc::Sender<Request>,
        output_chan: mpsc::Sender<Vec<Box<Device>>>,
        failure_chan: mpsc::Sender<Event>,
        pinger: Arc<dyn Pinger>,
        poll_interval_sec: i32,
        fping_timeout_ms: i32,
        fping_retries: i32,
        clock: Arc<dyn Clock>,
    ) -> Scheduler {
        Scheduler {
            queue: DeadlineQueue::new(),
            entity_req_chan,
            device_events,
            output_chan,
            failure_chan,
            pinger,
            poll_interval_sec,
            fping_timeout_ms,
            fping_retries,
            clock,
        }
    }

    pub fn init_queue(&mut self, device_ids: Vec<i64>, now: DateTime<Utc>) {
        self.queue.init_queue(device_ids, now);
    }

    pub async fn run(&mut self, _ctx: CancellationToken) {
        todo!("implemented by services-core agent")
    }

    pub fn process_device_event(&mut self, _event: &Event, _now: DateTime<Utc>) {
        todo!("implemented by services-core agent")
    }

    pub async fn schedule(&mut self, _ctx: CancellationToken, _now: DateTime<Utc>) {
        todo!("implemented by services-core agent")
    }

    pub fn queue_len(&self) -> usize {
        self.queue.len()
    }
}
