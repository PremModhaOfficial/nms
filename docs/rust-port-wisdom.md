# NMS Rust Port — Working Wisdom (the contract)

Branch: `refactor/rust`. Goal: faithful translation of the Go NMS (~7.4k LOC) into
idiomatic Rust with **Deterministic Simulation Testing (DST)**. Modes: tiger-style
**strict** (paired assertions, named bounds, zero warnings) + ponytail **ultra**
(minimal deps, YAGNI, stdlib first). Every agent MUST read this file before writing code.

---

## 0. Non-negotiable ground rules

1. **Rust idioms over Go structure.** Translate *behavior and semantics*, not syntax.
   No `unwrap()` on fallible paths; use `Result`. No `Box<dyn Any>` payloads; use enums.
2. **Tiger strict:** every function gets paired assertions (entry + exit), every loop
   and channel has a named bound, every branch has an `else`, zero compiler warnings.
   `assert!`/`debug_assert!` for invariants; `Result` for recoverable errors.
3. **Ponytail ultra:** stdlib before crates; no speculative abstraction; one-line
   solutions win. Mark deliberate shortcuts with `// ponytail: ...` naming the ceiling.
4. **DST is a first-class requirement:** services must be written so time, randomness,
   plugin execution, and DB access are *injectable* (traits). Tests run the real
   service code under deterministic simulated time (`tokio::time::pause`/`advance`),
   scripted effects, and a seeded RNG. See section 3.
5. **Behavioral parity with Go is the acceptance test.** Port every Go test to Rust
   (same expectations), plus new DST tests.
6. **Compatibility-critical formats must match Go exactly** (encryption layout, JWT
   claims, JSON shapes, SQL, fping flags, plugin stdin/stdout protocol). See section 4.

---

## 1. Patterns & anti-patterns

### 1.1 Rust language level

| Pattern | Anti-pattern |
|---|---|
| `Result<T, E>` + `?`; typed error enums via `thiserror` | `unwrap()`/`expect()` on external input; string errors |
| Enums + exhaustive `match` for domain variants (EventType, Operation, payloads) | Stringly-typed enums; `Box<dyn Any>` |
| `Option<T>` for optional values; `Option<&T>` for borrows | Null-like sentinels; `""` meaning "absent" |
| `&[T]` slices / `&str` for reads; owned `Vec`/`String` where owned needed | Needless `clone()` to appease the borrow checker |
| `Arc<Mutex<T>>`/`RwLock` for shared state; tokio `mpsc` for cross-task messages | `static mut`; leaked `Box::leak`; unbounded `mpsc::unbounded_channel` |
| Traits for polymorphism (injectable effects: `Clock`, `Pinger`, `Repository`) | One-implementation traits added speculatively (ponytail: skip) |
| `#[derive(serde::Serialize, Deserialize, sqlx::FromRow)]` | Hand-rolled JSON/SQL mapping |
| `const`/`const fn` for bounds; `assert!` on constants | Magic numbers scattered inline |
| `tokio::select! { biased; }` with `ctx.cancelled()` arm first | `select!` without cancellation arm (deadlock on shutdown) |
| `Duration` for time; `DateTime<Utc>` for wall timestamps | Raw `i64` timestamps; `Instant` stored across awaits |
| Small focused modules mirroring Go packages | One giant 2000-line `services.rs` |

### 1.2 Project level (our NMS)

**Keep (port faithfully):**
- Channel topology: same channel names, roles, and buffer sizes as Go (`cmd/app/main.go`
  `initServices`). Buffer sizes are named consts: `DATA_BUFFER_SIZE=1000`,
  `EVENT_BUFFER_SIZE=100`, `CONTROL_BUFFER_SIZE=50`.
- Request/Reply pattern: `models::Call` bounded by `RPC_TIMEOUT` (10s); API handlers
  use their own 5s `rpc_timeout` with `errServiceUnavailable`.
- Service responsibilities: EntityService (caches, CRUD, provisioning), Scheduler
  (deadline min-heap + fping), Poller (group-by-plugin, credential fetch), MetricsService
  (worker pool writes/reads), DiscoveryService (CIDR/range expansion), FailureService
  (sliding window deactivation).
- Validation rules, error classification (`classifyError` → 400/404/409/500), security
  headers, body cap (1 MiB), login throttle (5 fails, exp backoff, 4096 key cap).
- Trace store semantics: ring buffer (1000), finalize on root arrival, append late
  spans, `MaskJSON` sensitive keys (case-insensitive: payload, password, secret, token,
  authorization), 64 KiB body cap.

**Improve (Rust-idiomatic re-expression):**
- Go generics + reflection (`SqlxRepository[T]`, `RegisterEntityRoutes[T]`) → Rust
  traits/derives: `Repository<T>` trait + `sqlx::FromRow`; per-entity route fns.
- `interface{}` payloads → enums: `EventPayload`, `RequestPayload`, `ResponseData`.
- Goroutine + channel per service → tokio task + `mpsc`; reply channels → `oneshot`.
- Go `time.Time` zero checks → `Option<DateTime<Utc>>` where nullable; keep `DateTime`
  non-Option where schema is NOT NULL (created_at/updated_at).

**Never:**
- Blocking `std::thread::sleep` or `std::process::Command` inside async tasks (use
  `tokio::time`, `tokio::process` or `spawn_blocking`).
- Sending on a channel without a `select!` cancellation arm (shutdown deadlock).
- Unbounded loops/queues; every `loop` carries an explicit bound or is a documented
  event loop with `assert!(...)` that it is intentionally unbounded.
- Swallowing errors: log + return `Result`/empty-with-reason, matching Go behavior.
- String-building SQL with user input: parameterize everything (`QueryBuilder::push_bind`).

### 1.3 Library level (used crates)

| Crate | Do | Don't |
|---|---|---|
| `tokio` | `mpsc` bounded, `oneshot`, `time::{interval,sleep,timeout}`, `process::Command` + `kill_on_drop` + process-group kill (via `std::os::unix::process::CommandExt::process_group`), `signal::ctrl_c` | `mpsc::unbounded`; `process` without timeout/kill |
| `axum` 0.8 | `Router::route("/api/v1/devices/{id}", get(h).put(h2))`, `Path<i64>`, `State`, `middleware::from_fn`, `DefaultBodyLimit`, `Router::fallback` for dashboard | Route conflicts; `Extension` when `State` fits |
| `sqlx` 0.9 | features `runtime-tokio,postgres,derive,chrono,json` (NO `macros` → no compile-time DB); `QueryBuilder` for dynamic WHERE; `query_as::<T>` + `FromRow`; `PgPool` | `query!` macros (need DB at build); unbounded `.limit()` |
| `jsonwebtoken` 11 | `default-features=false, features=["rust_crypto"]`; HS256; claims struct with `exp, iat, iss="nms-lite", username`; `Validation` with `validate_exp` + required spec claims | Default features (aws-lc-rs build weight); alg confusion |
| `bcrypt` | `bcrypt::hash` (cost 10 default) / `verify` | Rolling your own |
| `aes-gcm` 0.11 | `Aes256Gcm`, 12-byte nonce, **nonce-prefixed hex layout identical to Go** | Different key/nonce layout (breaks stored data) |
| `chrono` | `DateTime<Utc>`, `serde` feature, RFC3339 | Local time in business logic |
| `rand` | seeded `StdRng::seed_from_u64` for DST; `thread_rng` prod | Unseeded global in tests |
| `tracing`/`tracing-subscriber` | JSON layer to stdout (parity with Go slog) | Interleaving logs in DST (keep assertions on state, not logs) |
| `rust-embed` | embed `web/` dashboard; serve via axum fallback | Reading web/ from disk at runtime |
| `winrm-rs` | plugin binary: `WinrmClient::new(cfg, creds)`, `run_powershell(host, script)`, `CommandOutput{stdout,stderr,exit_code}` | Sync blocking calls inside async |
| `libc` | process-group `kill(-pgid, SIGKILL)` on plugin timeout (mirror Go `syscall.Kill`) | Killing only the direct child (grandchildren leak) |
| `thiserror` | typed `Error` enums for api/database/plugin | `anyhow` in library code (main.rs may use it) |

---

## 2. Module layout (crates/nms)

```
crates/nms/src/
├── lib.rs            # pub mod ... + re-exports (integration tests use the lib)
├── main.rs           # thin: parse config, build services, run (mirrors cmd/app/main.go)
├── config.rs         # Config, LoadConfig, ValidateSecrets, FindFpingPath
├── models.rs         # entities, Event/Request/Response, ops, Call, span stamping
├── plugin.rs         # Task, Result (+ span ctx traits) — JSON contract with plugin binaries
├── crypto.rs         # AES-256-GCM encrypt/decrypt/DecryptPayload (Go-compatible)
├── database.rs       # Repository<T> trait, SqlxRepository<T>, Connect, error types
├── plugin_worker.rs  # generic PluginWorkerPool<T,R>, process execution
├── api/
│   ├── mod.rs        # router assembly: routes, middleware stack, JwtAuth
│   ├── routes.rs     # entity CRUD handlers, metrics handler, classifyError
│   ├── jwt.rs        # Auth, LoginHandler, JWTMiddleware, throttle
│   ├── middleware.rs # SecurityHeaders, MaxBodyBytes, TraceMiddleware
│   ├── response.rs   # writeJSON, respondError, capture writer
│   ├── provisioning.rs # RunDiscoveryHandler, ProvisionDeviceHandler
│   └── traces.rs     # TopologyHandler, TracesListHandler, TraceGetHandler
├── services/
│   ├── mod.rs
│   ├── scheduling.rs   # DeadlineQueue (BinaryHeap), Scheduler
│   ├── polling.rs      # Poller
│   ├── persistence.rs  # EntityService, MetricsService
│   ├── discovery.rs    # DiscoveryService, expandCIDR/expandRange
│   └── monitor_failure.rs # FailureService
└── tracex/
    ├── mod.rs        # Trace/Span/SpanEvent types, Start/Init/Default/Tracer
    ├── store.rs      # ring buffer Store
    ├── exporter.rs   # finalize-on-root-arrival exporter
    ├── topology.rs   # Topology() static graph
    └── mask.rs       # MaskJSON, sensitive keys, BodyEvent
crates/nms/tests/     # integration tests: DST harness + scenarios, api tests
crates/winrm-plugin/  # separate binary crate (winrm-rs), same stdin/stdout protocol
```

---

## 3. DST — Deterministic Simulation Testing (the requirement)

**What we mean:** run the *real* service code under a deterministic clock and
deterministic effects so that concurrency/timing bugs reproduce on every run.
No sleeps-in-test, no timing asserts on wall clock, no flaky waits.

**Engine (ponytail: no new deps — tokio gives it to us):**
- `#[tokio::test(start_paused = true)]` (feature `test-util`) → deterministic virtual
  time; `tokio::time::advance(Duration)` steps it exactly.
- Single-threaded current-thread runtime in tests → deterministic task scheduling.
- Injectable effects replace external I/O:
  - `trait Clock { fn now(&self) -> DateTime<Utc>; }` — prod: `SystemClock`; test: `SimClock`.
  - `trait Pinger { async fn ping(&self, ips: &[String]) -> HashMap<String,bool>; }`
    — prod: fping binary; test: scripted map.
  - `trait PluginRunner { async fn execute(&self, bin, tasks) -> Vec<plugin::Result>; }`
    — prod: spawn process; test: scripted results.
  - `Repository<T>` — prod: sqlx/Postgres; test: `MemRepository` (HashMap-backed).
- Seeded RNG: `rand::rngs::StdRng::seed_from_u64(n)` for trace/span ID generation
  (via `tracex`), so the whole run is reproducible (`DETERMINISTIC_SEED` env).

**Harness shape (crates/nms/tests/dst/):**
- `harness.rs`: build the full channel topology exactly like Go `initServices`, but with
  scripted `Pinger`/`PluginRunner` and `MemRepository`. Expose `advance(secs)` and
  `run_until(predicate)` helpers using `tokio::time::advance` + `yield_now` pumping.
- Scenario files: `scheduler.rs`, `poller.rs`, `failure.rs`, `metrics.rs`,
  `discovery.rs`, `shutdown.rs` — each asserts exact state transitions (e.g. "after 3
  failures within window, exactly one deactivate request", "device polled exactly once
  per tick despite duplicate queue entries").
- Port every Go unit test too (in-module `#[cfg(test)]`), same expectations.

**DST anti-patterns:** real sleeps, real network, real plugin binaries in tests,
unseeded randomness, asserting on log output, `tokio::time::sleep` in test code that
waits for real time to pass.

---

## 4. Compatibility contracts (must match Go byte-for-byte where noted)

1. **Encryption** (`crypto.rs`): AES-256-GCM, key = 64 hex chars → 32 bytes; nonce =
   12 random bytes; ciphertext = hex(nonce ‖ ct). `EncryptStruct`/`DecryptStruct` touch
   only `payload` field. `DecryptPayload`: on failure, if `APP_ENV != production` and
   payload starts with `{`, return raw (dev fallback).
2. **JWT**: HS256, claims `{username, iss:"nms-lite", exp, iat}`; middleware requires
   `Authorization: Bearer <t>`, HS256 only, iss `nms-lite`, username non-empty.
3. **Plugin protocol**: stdin = JSON array of `Task{device_id?,target,port,credentials?,trace_id?,span_id?}`;
   stdout = JSON array of `Result{device_id?,target,port,success,error?,hostname?,data?,trace_id?,span_id?}`.
   Pool args: polling pool runs with no args; discovery pool runs with `-discovery`.
4. **fping**: `fping -a -q -t <timeout_ms> -r <retries> <ips...>`; stdout lines =
   reachable IPs. Non-zero exit is normal (some hosts down).
5. **SQL**: identical queries to Go (schema.sql unchanged). Repository `Create`:
   `INSERT ... RETURNING *` skipping id/created_at/updated_at; `Update`:
   `UPDATE ... SET <set>, updated_at=NOW() WHERE id=$n RETURNING *` with
   `update:"omitempty"` semantics (skip zero values for tagged fields; always skip
   zero timestamps). `GetByFields` validates column allowlist first (SQL injection).
6. **Metrics write**: chunked batch insert (1000/batch) into `metrics(device_id,data,timestamp)`
   — sqlx has no CopyFrom; chunked multi-row INSERT is the ponytail equivalent
   (mark `// ponytail: chunked INSERT vs pgx CopyFrom, add COPY when volume demands`).
   Metrics read: `SELECT timestamp, data #> '{<path>}' as value FROM metrics WHERE
   device_id=$1 AND timestamp>=$2 AND timestamp<=$3 ORDER BY timestamp DESC LIMIT $4`;
   path validated `^[a-zA-Z][a-zA-Z0-9_]{0,31}(\.[a-zA-Z][a-zA-Z0-9_]{0,31})*$`, ≤128 chars.
7. **HTTP**: routes, status codes, error JSON shape `{"error":{"message","status"}}`,
   security headers, CSP for dashboard, body cap 1 MiB → 400, HSTS only on TLS.
8. **Trace/dashboard JSON**: `Trace{trace_id,root_span_id,started_at,ended_at,duration_ms,
   method?,path?,status_code?,component_ids,span_count,error,spans[]}` and
   `Span{span_id,parent_id?,name,kind,component,started_at,ended_at,duration_ms,attributes?,events?}`
   — this is the frontend contract (web/ dashboard); do not rename fields.
9. **WinRM plugin**: flags `-discovery -timeout 60s -insecure`; TLS default port 5986,
   plaintext 5985 only with `-insecure`; domain → `DOMAIN\user` NTLM; polling runs
   the metrics PowerShell script via `-EncodedCommand` (UTF-16LE base64); discovery
   runs `hostname`; bounds: maxConcurrent 32, maxStdinBytes 64 MiB, maxInputsPerBatch 10000.
10. **Scheduler/failure logic**: deadline = now + polling_interval_seconds; duplicate
    queue entries deduped by EntityService batch handler; failure window/threshold
    (default 3 min / 3); deactivate on threshold → status inactive → no longer polled.

---

## 5. Agent deliverable checklist

Each module lands with:
- `// tiger:` comments naming assertions + bounds added.
- `// ponytail:` comments naming deliberate shortcuts.
- Unit tests ported from the corresponding Go `_test.go` (same cases/expectations).
- `cargo build` / `cargo test` green for the crate (agents may create the crate and
  `cargo check` their module via `cargo test -p nms` once the workspace exists).
- No `unwrap()` on fallible paths; no warnings (`cargo build` clean).


---

## 6. Integration contract — exact signatures (agents MUST match these)

Services and api are written against these signatures; main.rs and the DST
harness depend on them. `mpsc::Sender`/`Receiver` = `tokio::sync::mpsc`; ctx =
`tokio_util::sync::CancellationToken`; `Clock` = `crate::services::clock::Clock`.

### services/scheduling.rs
```rust
pub struct Scheduler { /* queue: BinaryHeap<DeviceDeadline>, channels, fping config */ }
pub struct DeviceDeadline { pub device_id: i64, pub deadline: DateTime<Utc> } // Ord by deadline

impl Scheduler {
    pub fn new(
        device_events: mpsc::Receiver<Event>,
        entity_req_chan: mpsc::Sender<Request>,
        output_chan: mpsc::Sender<Vec<Box<Device>>>,
        failure_chan: mpsc::Sender<Event>,
        pinger: Arc<dyn Pinger>,          // from plugin_worker
        poll_interval_sec: i32,
        fping_timeout_ms: i32,
        fping_retries: i32,
        clock: Arc<dyn Clock>,
    ) -> Scheduler;
    pub fn init_queue(&mut self, device_ids: Vec<i64>, now: DateTime<Utc>);
    pub async fn run(&mut self, ctx: CancellationToken);
    pub fn process_device_event(&mut self, event: &Event, now: DateTime<Utc>); // queue push (create/update)
    pub async fn schedule(&mut self, ctx: CancellationToken, now: DateTime<Utc>); // full tick
    pub fn queue_len(&self) -> usize;      // test hook
}
```
DeadlineQueue = `BinaryHeap<DeviceDeadline>` (min-heap by deadline) with
`PopExpired(now) -> Vec<DeviceDeadline>`, `PushBatch`, `InitQueue`. `schedule`
logic mirrors Go: pop expired → `OpGetBatch` call → fping ToPing → qualify →
failure events → requeue with deadline+interval → dispatch qualified.

### services/polling.rs
```rust
pub struct Poller { /* pool, plugins: HashMap<String,String>, encryption key, channels */ }
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
    ) -> (Poller, mpsc::Receiver<Vec<plugin::Result>>); // second = pool results rx
    pub async fn run(&mut self, ctx: CancellationToken);
    pub fn load_plugins(&mut self); // scan plugin_dir, map plugin_id -> bin path
    pub fn group_by_protocol(&self, devices: &[Box<Device>]) -> HashMap<String, Vec<Box<Device>>>;
    pub async fn get_credential(&self, ctx: &CancellationToken, profile_id: i64) -> Option<Box<CredentialProfile>>;
}
```

### services/persistence.rs (EntityService + MetricsService)
```rust
pub struct EntityService { /* repos, caches, event channels, clock */ }
impl EntityService {
    pub fn new(
        discovery_results: mpsc::Receiver<plugin::Result>,
        events_chan: mpsc::Receiver<Event>,
        requests: mpsc::Receiver<Request>,
        credential_repo: Arc<dyn Repository<CredentialProfile>>,
        device_repo: Arc<dyn Repository<Device>>,
        discovery_profile_repo: Arc<dyn Repository<DiscoveryProfile>>,
        discovery_profile_events: mpsc::Sender<Event>,
        device_events: mpsc::Sender<Event>,
        clock: Arc<dyn Clock>,
    ) -> EntityService;
    pub async fn run(&mut self, ctx: CancellationToken);          // 3-channel loop + cache backfill
    pub async fn load_caches(&mut self) -> Result<(), String>;
    pub fn get_active_device_ids(&self) -> Vec<i64>;
    pub async fn handle_crud_request(&mut self, req: Request);    // replies via req.reply
    pub fn handle_get_batch(&self, ids: Vec<i64>) -> BatchDeviceResponse; // cache-only, dedup
    pub fn handle_get_credential(&self, id: i64) -> Option<Box<CredentialProfile>>;
    pub async fn handle_deactivate_device(&mut self, device_id: i64) -> Result<Box<Device>, String>;
    pub fn device_cache_len(&self) -> usize;                      // test hook
}

pub trait MetricsStore: Send + Sync {   // DST: MemMetricsStore; prod: sqlx
    async fn insert_batch(&self, rows: Vec<(i64, serde_json::Value, DateTime<Utc>)>) -> Result<(), DbError>;
    async fn query(&self, device_id: i64, path: &str, start: DateTime<Utc>, end: DateTime<Utc>, limit: i32)
        -> Result<Vec<MetricResult>, DbError>;
}

pub struct MetricsService { /* poll_results rx, query reqs rx, stores, worker pool, failure chan */ }
impl MetricsService {
    pub fn new(
        poll_results: mpsc::Receiver<Vec<plugin::Result>>,
        query_reqs: mpsc::Receiver<Request>,
        write_store: Arc<dyn MetricsStore>,
        read_store: Arc<dyn MetricsStore>,
        worker_count: i32,
        failure_chan: mpsc::Sender<Event>,
        default_limit: i32,
        default_range_hours: i32,
        clock: Arc<dyn Clock>,
    ) -> MetricsService;
    pub async fn run(&mut self, ctx: CancellationToken);
    pub fn validate_path(path: &str) -> Result<(), String>;       // regex + 128 cap
}
```

### services/discovery.rs
```rust
pub struct DiscoveryService { /* pool, pending map, channels, clock */ }
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
    ) -> (DiscoveryService, mpsc::Receiver<Vec<plugin::Result>>);
    pub async fn start(&mut self, ctx: CancellationToken);  // event loop + result collector
    pub fn run_discovery(&mut self, profile: &DiscoveryProfile);  // expand, decrypt, submit chunks
    pub fn expand_target(target: &str) -> Result<Vec<String>, String>; // pub pure fns for tests
    pub fn expand_cidr(cidr: &str) -> Result<Vec<String>, String>;
    pub fn expand_range(range: &str) -> Result<Vec<String>, String>;
}
// Bounds: MAX_EXPAND_HOSTS=65536, MAX_TASKS_PER_BATCH=10000, PENDING_STALE_AFTER=30min
```

### services/monitor_failure.rs
```rust
pub struct FailureService { /* failures: HashMap<i64, FailureRecord>, window, threshold, clock */ }
pub struct FailureRecord { pub last_time: DateTime<Utc>, pub count: i32 }
impl FailureService {
    pub fn new(
        failure_chan: mpsc::Receiver<Event>,
        entity_req_chan: mpsc::Sender<Request>,
        window_min: i32,
        threshold: i32,
        clock: Arc<dyn Clock>,
    ) -> FailureService;
    pub async fn run(&mut self, ctx: CancellationToken);
    pub fn handle_failure(&mut self, event: &DeviceFailureEvent) -> Option<i64>; // Some(device) when threshold hit
    pub fn failure_count(&self, device_id: i64) -> i32;   // test hook
}
```

### plugin_worker.rs
```rust
#[async_trait]
pub trait Pinger: Send + Sync {
    async fn ping(&self, ips: &[String]) -> HashMap<String, bool>;
}
#[async_trait]
pub trait PluginRunner: Send + Sync {
    /// Execute a plugin binary with args over tasks (JSON stdin/stdout).
    async fn execute(&self, bin_path: &str, args: &[String], tasks: &[plugin::Task]) -> Vec<plugin::Result>;
}
pub struct ProcessPluginRunner { /* prod: spawn + 5min timeout + process-group kill */ }
impl ProcessPluginRunner { pub fn new() -> Arc<dyn PluginRunner>; }
pub struct ProcessPinger { /* prod: fping -a -q -t -r */ }
impl ProcessPinger { pub fn new(fping_path: String, timeout_ms: i32, retries: i32) -> Arc<dyn Pinger>; }

pub struct PluginWorkerPool<T, R> { /* job mpsc, worker tasks, runner */ }
impl<T, R> PluginWorkerPool<T, R>
where T: Send + 'static + serde::Serialize, R: Send + 'static + serde::de::DeserializeOwned
{
    pub fn new(worker_count: usize, pool_name: &str, buffer_size: usize, runner: Arc<dyn PluginRunner>)
        -> (PluginWorkerPool<T, R>, mpsc::Receiver<Vec<R>>); // panics on worker_count<=0 or buffer<0
    pub fn start(&mut self, ctx: CancellationToken);
    pub fn submit(&self, bin_path: &str, tasks: Vec<T>) -> bool; // false when cancelled
}
```
Prod runner must: marshal tasks JSON → spawn with process_group(0) → timeout
5 min → kill(-pgid, SIGKILL) on timeout/cancel (libc) → parse stdout JSON array.

### api/ (mod.rs router assembly + routes/jwt/middleware/response/provisioning/traces)
```rust
#[derive(Clone)]
pub struct ApiChannels {
    pub crud_request: mpsc::Sender<Request>,
    pub metric_request: mpsc::Sender<Request>,
    pub provisioning_event: mpsc::Sender<Event>,
}

pub struct JwtAuth { /* secret, admin user/hash, expiry, throttle state */ }
impl JwtAuth {
    pub fn new(cfg: &config::Config) -> Result<JwtAuth, String>;  // asserts non-empty secret/hash, bcrypt-parseable, expiry>=1
    pub fn login_handler(&self) -> impl Handler;                  // POST /login
    pub fn jwt_middleware(&self) -> ...;                          // Bearer HS256 iss nms-lite
}

pub fn build_router(cfg: &config::Config, auth: JwtAuth, ch: ApiChannels, tracer: Arc<Tracer>) -> Router;

// routes.rs: entity CRUD handlers (per entity, no reflection — explicit fns),
// metrics handler, classify_error -> ApiError, do_request (5s rpc_timeout).
// middleware.rs: SecurityHeaders, MaxBodyBytes(1MiB -> 400), TraceMiddleware.
// response.rs: write_json, respond_error, capture writer (64 KiB JSON/text).
// provisioning.rs: RunDiscoveryHandler, ProvisionDeviceHandler.
// traces.rs: TopologyHandler, TracesListHandler (limit 1..=200, default 50), TraceGetHandler.
```

### main.rs (written by the orchestrator; agents don't touch)
Mirrors Go cmd/app/main.go: logger → config → validate secrets/TLS → tracex init
→ db connect → channels → services → load caches → init queue → run services →
router → HTTP (TLS when cert+key) → graceful shutdown.

### DST harness (crates/nms/tests/dst/, written by orchestrator)
Builds the full topology with scripted Pinger/PluginRunner, MemRepository,
MemMetricsStore, ManualClock; drives time with `tokio::time::advance` + clock
advance; asserts exact state transitions. Agents keep service logic free of
real time/process calls so this works.

---
## 7. Agent assignments (parallel)

| Agent | Files to write | Ports from (Go) |
|---|---|---|
| A: database | `crates/nms/src/database/sqlx_repo.rs` (SqlxRepository<T> for CredentialProfile/Device/DiscoveryProfile, Connect/ConnectRaw, ColumnNames, HasId impls, Repository impls for MemRepository) | pkg/database/{db,repository}.go + repository_test.go |
| B: plugin_worker | `crates/nms/src/plugin_worker.rs` | pkg/pluginWorker/pool.go + pool_test.go |
| C: services-core | `services/scheduling.rs`, `services/polling.rs` | monitorScheduler.go, deadlinePriorityQueue.go, metricsPoller.go |
| D: services-persistence | `services/persistence.rs` | entityService.go, metricsService.go + their tests |
| E: services-discovery-health | `services/discovery.rs`, `services/monitor_failure.rs` | discoveryService.go, healthMonitor.go + tests |
| F: api | `api/{mod,routes,jwt,middleware,response,provisioning,traces}.rs` | pkg/api/* + api_test.go, encryption_test.go (crypto already ported) |
| G: winrm-plugin | `crates/winrm-plugin/src/main.rs` | plugin-code/winrm/main.go + main_test.go |


---

## 8. Flow tests (NEW requirement — every agent delivers one)

Each agent writes ONE integration test file that walks its module's complete
lifecycle as a **flow** (Postman-collection style: step → state change →
assertion → next step). Model the shape on the example flow doc
`de-postman/discovery-engine-import-single-replica-options.html`: a numbered
sequence where each step performs one real operation through the module's
public interface, asserts the expected outcome AND the resulting state, then
moves to the next step. The flow must cover the module's end-to-end journey,
not isolated unit cases.

- **api agent** → `crates/nms/tests/api_flow.rs`: HTTP flow through the real
  axum router with MemRepository + scripted services: login (bad → throttle →
  good) → create credential → create discovery profile → run discovery trigger
  → list devices → update device → metrics query (with path validation) →
  delete. Assert status codes + JSON bodies + channel traffic at each step.
- **database agent** → `crates/nms/tests/db_flow.rs`: repository lifecycle for
  one entity against MemRepository AND (when a DATABASE_URL is present) the
  sqlx repo: create (id assigned) → get → get_by_fields → update (omitempty
  semantics) → list → delete → not-found. Run against sqlx only if
  `NMS_TEST_DATABASE_URL` is set; MemRepository flow always runs.
- **plugin_worker agent** → `crates/nms/tests/pool_flow.rs`: pool lifecycle:
  start → submit batch → results round-trip → submit while shutting down
  (rejected) → cancel → results channel closes. Use shell-script fake plugins
  (as Go pool_test does) plus a scripted PluginRunner for the DST variant.
- **services-core agent** → `crates/nms/tests/scheduler_flow.rs`: full tick
  flow with scripted Pinger: init queue → device event (create) → advance time
  → tick → get_batch (dedup) → fping split (reachable/not) → failure event for
  unreachable → qualified dispatch → requeue with new deadline → next tick.
- **services-persistence agent** → `crates/nms/tests/persistence_flow.rs`:
  EntityService CRUD + cache flow with MemRepository (create → cache → event;
  deactivate → status inactive → event) AND MetricsService flow (poll results
  → write store rows → query path validation → failure event on poll error).
- **services-discovery-health agent** → `crates/nms/tests/discovery_health_flow.rs`:
  discovery flow (create profile event → expand target → submit tasks →
  scripted result → provision device via EntityService-like path) and health
  flow (failures within window → threshold → deactivate request sent).
- **winrm-plugin agent** → `crates/winrm-plugin/tests/plugin_flow.rs`: stdin
  JSON batch → stdout JSON results (empty target error, missing credentials
  error, invalid port error, default TLS port) — drive `main`-level logic
  through the process function directly (no real WinRM network in CI).

Flow tests run under the DST harness where services are involved:
`#[tokio::test(start_paused = true)]`, ManualClock, scripted effects. Each
flow test asserts EXACT state after each step (no sleeps, no flaky waits).


---

## 9. Flow design documents (HTML, same depth as the example) — every agent

Each agent ALSO writes a standalone HTML flow design document for its module,
at the SAME LEVEL OF DEPTH as the reference example:
`de-postman/discovery-engine-import-single-replica-options.html` (a rich
single-file HTML with inline mermaid diagrams and color-coded nodes). Match its
structure and depth EXACTLY:

1. **Title + "question on the table"** — one paragraph framing the design
   question the flow answers (e.g., for scheduling: "one scheduler tick
   currently fans out to N devices through a shared pool — should a tick own
   its whole batch in-process instead?").
2. **Current flow** — prose walking the flow end-to-end with concrete numbers
   (buffer sizes, timeouts, worker counts, costs: "Cost: ... exists only to
   ..."), followed by a `flowchart LR` mermaid diagram with subgraphs for the
   boundaries in your module (service ↔ channels ↔ worker pool ↔ DB/plugin),
   and color-coded `style` lines on the key node.
3. **Three candidate flows** — Option A (simplest, do-less), Option B
   (industry default), Option C (isolation/future). For EACH: a one-word
   tagline, prose explaining step-by-step behavior, what it DELETES, what it
   ADDS, its tradeoff, and its own `flowchart LR` mermaid diagram with
   subgraphs + colored style. Make each option a REAL alternative with
   genuinely different tradeoffs, not a strawman.
4. **How the industry solves this exact problem** — a table: Pattern |
   Who / where | Maps to (which option). 5–8 rows, grounded in real systems
   (e.g. Kubernetes scheduler, Temporal/Sidekiq, AWS SQS visibility timeout,
   Zabbix/Nagios health windows, Ansible WinRM, sidecar pattern, CQRS).
5. **Decision ladder** — A → B → C upgrade ladder: when to pick each, and
   what EVERY option removes compared to current (the shared complexity).

Technical depth requirements: use the module's real constants and channels
(buffer sizes, RPC_TIMEOUT, pluginExecTimeout, MAX_EXPAND_HOSTS, thresholds);
reference the real component names from the module's Go source; the mermaid
diagrams must render (valid mermaid syntax, `&lt;br/&gt;` for line breaks,
subgraph blocks, `style X fill:#hex,color:#fff`).

Write to `crates/nms/docs/flows/<module>-flow.html` (create dir). Keep the
HTML self-contained (inline <style> + <script src="https://cdn.jsdelivr.net/npm/mermaid..."> for rendering, like the example). ~2–3k lines of rich HTML expected, mirroring the example's density.

Per-module flow subjects (frame the design question yourself from the Go code):
- api: request lifecycle through middleware → router → channel RPC → service.
- database: dynamic SQL CRUD (reflection-free) — query build strategy.
- plugin_worker: plugin execution (subprocess JSON) — isolation options.
- scheduling/polling: scheduler tick → fping → poller → pool fan-out.
- persistence: entity cache + metrics write/read (CopyFrom equivalent).
- discovery/health: CIDR expansion + pending tracking; failure window.
- winrm-plugin: batch WinRM execution protocol.
