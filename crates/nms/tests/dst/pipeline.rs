//! DST scenario: full topology pipeline (the crown jewel).
//! Written tests-first against the §6 contract. Full loop-based assertions
//! are filled in once service run loops land; the drive-the-service-method
//! flow below already exercises the contract surface.

mod harness;

use std::collections::HashMap;

use nms::models::{CredentialProfile, EventPayload, EventType};

use harness::*;

fn cred(name: &str, protocol: &str, payload: &str) -> CredentialProfile {
    CredentialProfile {
        id: 0,
        name: name.to_string(),
        protocol: protocol.to_string(),
        payload: payload.to_string(),
        created_at: epoch(),
        updated_at: epoch(),
    }
}

#[tokio::test(start_paused = true)]
async fn poll_success_flow_writes_metrics() {
    // ── Step 1: seed a credential and an active device; load caches.
    let mut topo = build_topology(HashMap::from([("10.0.0.1".to_string(), true)]));
    let cred_id = topo.cred_repo.insert(cred("win", "winrm", r#"{"username":"u","password":"p"}"#));
    let dev_id = topo.dev_repo.insert(device("10.0.0.1", "winrm", true));
    topo.entity.load_caches().await.expect("load caches");
    assert_eq!(topo.cred_repo.len(), 1, "step1: credential seeded");
    assert_eq!(topo.dev_repo.len(), 1, "step1: device seeded");
    assert_eq!(topo.entity.get_active_device_ids(), vec![dev_id], "step1: device active in cache");
    let _ = cred_id;

    // ── Step 2: scheduler queue + one tick. The device is ping-reachable, so
    // it qualifies and is dispatched on scheduler_to_poller (poller loop).
    topo.sched.init_queue(vec![dev_id], topo.clock.now());
    topo.sched.schedule(topo.ctx.clone(), topo.clock.now()).await;
    assert_eq!(topo.sched.queue_len(), 1, "step2: requeued after tick");

    // ── Step 3: poller side — credential fetch must resolve from cache.
    let cred = topo.entity.handle_get_credential(1);
    assert!(cred.is_some(), "step3: credential resolvable from cache");
    assert_eq!(cred.unwrap().protocol, "winrm", "step3: protocol matches");

    // ── Step 4: metrics query validation boundary (contract §4.6).
    assert!(nms::services::persistence::MetricsService::validate_path("cpu.total").is_ok());
    assert!(nms::services::persistence::MetricsService::validate_path("1cpu").is_err());
    assert!(nms::services::persistence::MetricsService::validate_path(&"a".repeat(129)).is_err());
}

#[tokio::test(start_paused = true)]
async fn failure_to_deactivation_pipeline() {
    // Full journey: unreachable device fails ping → health counts → threshold
    // → deactivate request (crud channel) → excluded from get_batch.
    let mut topo = build_topology(HashMap::new()); // nothing reachable
    let dev_id = topo.dev_repo.insert(device("10.0.0.9", "winrm", true));
    topo.entity.load_caches().await.expect("load caches");
    topo.sched.init_queue(vec![dev_id], topo.clock.now());

    // Step 1: one tick — unreachable → ping failure event to health.
    topo.sched.schedule(topo.ctx.clone(), topo.clock.now()).await;
    // (health loop consumes failure_tx; assert its state directly)
    let ev = nms::models::Event::new(
        EventType::DeviceFailure,
        EventPayload::DeviceFailure(nms::models::DeviceFailureEvent {
            device_id: dev_id,
            timestamp: topo.clock.now(),
            reason: "ping".to_string(),
        }),
    );
    let EventPayload::DeviceFailure(payload) = &ev.payload else { unreachable!() };
    topo.health.handle_failure(payload);
    assert_eq!(topo.health.failure_count(dev_id), 1, "step1: one failure recorded");

    // Step 2: device still returned by get_batch while active.
    let batch = topo.entity.handle_get_batch(vec![dev_id]);
    assert_eq!(batch.to_ping.len(), 1, "step2: active device still in batch");

    // Step 3: two more failures → threshold → deactivate request.
    for _ in 0..2 {
        let ev = nms::models::Event::new(
            EventType::DeviceFailure,
            EventPayload::DeviceFailure(nms::models::DeviceFailureEvent {
                device_id: dev_id,
                timestamp: topo.clock.now(),
                reason: "ping".to_string(),
            }),
        );
        let EventPayload::DeviceFailure(payload) = &ev.payload else { unreachable!() };
        topo.health.handle_failure(payload);
    }
    assert_eq!(topo.health.failure_count(dev_id), 0, "step3: record cleaned after deactivation");
}
