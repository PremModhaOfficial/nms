//! DST scenario: Scheduler tick flow (scheduling.rs contract §6).
//! Flow: seed queue → device event → advance to tick → get_batch (dedup) →
//! fping split (reachable/unreachable) → failure event → qualified dispatch →
//! requeue with deadline+interval → next tick.

mod harness;

use std::collections::HashMap;

use nms::models::{Event, EventPayload, EventType};

use harness::*;

fn seed_device(topo: &Topology, ip: &str, should_ping: bool) -> i64 {
    topo.dev_repo.insert(device(ip, "winrm", should_ping))
}

#[tokio::test(start_paused = true)]
async fn scheduler_full_tick_flow() {
    let reachable = HashMap::from([("10.0.0.1".to_string(), true)]);
    let mut topo = build_topology(reachable);
    let now = topo.clock.now();

    // ── Step 1: seed two devices (one reachable, one not) into cache + queue.
    let ping_id = seed_device(&topo, "10.0.0.1", true);
    let dead_id = seed_device(&topo, "10.0.0.2", true);
    assert_eq!(topo.dev_repo.len(), 2, "step1: two devices seeded");
    topo.sched.init_queue(vec![ping_id, dead_id], now);
    assert_eq!(topo.sched.queue_len(), 2, "step1: queue holds both");

    // ── Step 2: device create event adds a duplicate entry (lazy queue).
    let dev = topo
        .dev_repo
        .snapshot()
        .into_iter()
        .find(|(id, _)| *id == ping_id)
        .expect("seeded device present")
        .1;
    topo.sched.process_device_event(
        &Event::new(EventType::Create, EventPayload::Device(Box::new(dev))),
        now,
    );
    assert_eq!(topo.sched.queue_len(), 3, "step2: duplicate entry tolerated");

    // ── Step 3: one schedule pass at t=0 (all deadlines due). EntityService
    // batch handler dedups, so the reachable device is dispatched and the
    // unreachable one produces a ping failure on failure_tx.
    topo.sched.schedule(topo.ctx.clone(), now).await;

    // ── Step 4: both requeued with deadline = now + 60s polling interval.
    assert_eq!(topo.sched.queue_len(), 2, "step4: both requeued after tick");
    let t60 = now + chrono::Duration::seconds(60);
    let early = topo.sched.queue.pop_expired(t60 - chrono::Duration::milliseconds(1));
    assert!(early.is_empty(), "step4: nothing expires before deadline");
    let at = topo.sched.queue.pop_expired(t60);
    assert_eq!(at.len(), 2, "step4: both expire exactly at deadline");

    // ── Step 5: second tick at t=60 repeats the split.
    topo.sched.schedule(topo.ctx.clone(), t60).await;
    assert_eq!(topo.sched.queue_len(), 2, "step5: requeued again");
}
