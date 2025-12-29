# NMS Project Mirror

This document provides a 1-to-1 declarative summary of every file in the NMS project.

---

## Project Root

### `Makefile`
- **Targets**:
  - `build`: Compiles `bin/nms-server` and `plugins/winrm`.
  - `run`: Executes `start.sh`.
  - `seed`: Runs `seed.py` for database seeding.
  - `db-setup`: Executes `schema.sql` via psql.
  - `first-run`: Runs `stop`, `db-setup`, `build`, `run` sequentially.
  - `dev`: Runs `stop`, `build`, and `start.sh`.
  - `stop`: Kills running `nms-server` and port 8080 processes.
  - `clean`: Removes `bin/` directory.

### `schema.sql`
- **Tables**:
  - `credential_profiles`: `id`, `name`, `protocol`, `payload` (AES-encrypted), timestamps.
  - `discovery_profiles`: `id`, `name`, `target` (CIDR/IP), `port`, `credential_profile_id` FK, `auto_provision`, timestamps.
  - `devices`: `id`, `hostname`, `ip_address` (INET), `plugin_id`, `port`, `credential_profile_id` FK, `discovery_profile_id` FK, `polling_interval_seconds`, `should_ping`, `status`, timestamps.
  - `metrics`: `id`, `device_id`, `data` (JSONB), `timestamp`.
- **Indexes**:
  - `idx_metrics_device_time`: `(device_id, timestamp DESC)`.
  - `idx_devices_status`: `(status)`.
  - `idx_devices_ip_port`: Unique on `(ip_address, port)`.

### `start.sh`
- Compiles server and WinRM plugin to `bin/` and `plugins/`.
- Sets `NMS_ADMIN_USER` (default: "admin").
- If `ADMIN_PASSWORD` is set, generates bcrypt hash via `scripts/hashpassword.go`.
- Sets `JWT_SECRET` (default: random via openssl).
- Sets `ENCRYPTION_KEY` (default: hardcoded dev key).
- Executes `bin/nms-app`.

### `app.yaml`
- Viper defaults for database, workers, intervals, pool sizes, secrets.

### `seed.py`
- Waits for server, logs in, creates credential profile and discovery profile.

---

## `cmd/app/main.go`

### Structs
- `services`: Holds `Scheduler`, `Poller`, `DiscoveryService`, `MetricsService`, `EntityService`, `FailureService`.
- `apiChannels`: Holds `crudRequest`, `metricRequest`, `provisioningEvent` channels.

### Constants
- `DataBufferSize = 1000`, `EventBufferSize = 100`, `ControlBufferSize = 50`.

### Functions
- `main()`: Calls `initLogger`, `loadConfig`, `initDatabase`, `initServices`, `loadInitialData`, `startServices`, `initRouter`. Blocks on `ctx.Done()` for graceful shutdown.
- `initLogger()`: Configures `slog` with JSON handler.
- `loadConfig()`: Calls `config.LoadConfig(".")`.
- `initDatabase()`: Calls `database.Connect(conf)` returning `*sqlx.DB`.
- `initServices()`: Creates channels, creates `EntityService`, `Scheduler`, `Poller`, `MetricsService` (with separate write/read DB pools via `database.ConnectRaw`), `DiscoveryService`, `HealthMonitor`. Returns `*services` and `*apiChannels`.
- `loadInitialData()`: Calls `entityService.LoadCaches()` and `sched.InitQueue()` with active device IDs.
- `startServices()`: Starts 6 goroutines for each service's `Run()` or `Start()`.
- `initRouter()`: Configures Gin router with `/login` (public), `/api/v1/*` routes (protected by JWT middleware). Registers generic entity routes for `CredentialProfile`, `Device`, `DiscoveryProfile`, plus `/metrics`, `/discovery_profiles/:id/run`, `/devices/:id/provision`.

---

## `pkg/config/config.go`

### Struct `Config`
- Fields: DB connection, TLS, `PluginsDir`, worker counts, scheduler intervals, security keys, session duration, metrics defaults, connection pool settings, failure monitor settings.

### Functions
- `LoadConfig(path)`: Sets defaults, reads `app.yaml`, binds environment variables, returns `*Config`.
- `ValidateSecrets()`: Returns error if JWT_SECRET or ENCRYPTION_KEY are default values.
- `FindFpingPath()`: Uses `exec.LookPath("fping")`.

---

## `pkg/database/db.go`

### Functions
- `Connect(cfg)`: Opens `sqlx.DB` with `pgx` driver, configures pool settings.
- `ConnectRaw(cfg, poolName, maxOpen, maxIdle)`: Opens raw `sql.DB` for high-performance operations (MetricsWriter/Reader).

---

## `pkg/database/repository.go`

### Interface `Repository[T]`
- Methods: `List`, `Get`, `GetByFields`, `Create`, `Update`, `Delete`.

### Struct `SqlxRepository[T]`
- Field: `db *sqlx.DB`.

### Methods
- `tableName()`: Returns `T.TableName()`.
- `DB()`: Returns underlying `*sqlx.DB`.
- `List()`: `SELECT * FROM table`.
- `Get(id)`: `SELECT * FROM table WHERE id = $1`.
- `GetByFields(filters)`: Dynamic WHERE clause.
- `Create(entity)`: `INSERT ... RETURNING *` with `buildInsertParts`.
- `Update(id, entity)`: `UPDATE ... SET ... RETURNING *` with `buildUpdateParts`.
- `Delete(id)`: `DELETE FROM table WHERE id = $1`.

### Helper Functions
- `buildInsertParts(entity)`: Uses reflection to build column list, placeholders, values. Skips `id`, `created_at`, `updated_at`, `db:"-"`.
- `buildUpdateParts(entity)`: Skips auto fields and zero values for `update:"omitempty"` tags.
- `isZeroValue(v)`: Checks if reflect.Value is zero.

---

## `pkg/models/models.go`

### Structs
- `Metric`: `id`, `device_id`, `data` (json.RawMessage), `timestamp`. Table: `metrics`.
- `CredentialProfile`: `id`, `name`, `protocol`, `payload` (`gocrypt:"aes"`), timestamps. Table: `credential_profiles`.
- `DiscoveryProfile`: `id`, `name`, `target`, `port`, `credential_profile_id`, `auto_provision`, timestamps, `CredentialProfile *` (cache lookup). Table: `discovery_profiles`.
- `Device`: `id`, `hostname`, `ip_address`, `plugin_id`, `port`, `credential_profile_id`, `discovery_profile_id`, `polling_interval_seconds`, `should_ping`, `status`, timestamps, `CredentialProfile *`, `DiscoveryProfile *` (cache lookups). Table: `devices`.
- `MetricQuery`: `path`, `start`, `end`, `limit`.

---

## `pkg/models/event.go`

### Type `EventType`
- Constants: `EventCreate`, `EventUpdate`, `EventDelete`, `EventTriggerDiscovery`, `EventProvisionDevice`, `EventDeviceFailure`, `EventRunDiscovery`.

### Structs
- `Event`: `Type`, `Payload interface{}`.
- `DiscoveryTriggerEvent`: `DiscoveryProfileID`.
- `DeviceProvisionEvent`: `DeviceID`, `PollingIntervalSeconds`.
- `DeviceFailureEvent`: `DeviceID`, `Timestamp`, `Reason` ("ping" or "poll").

---

## `pkg/models/request.go`

### Constants
- `OpList`, `OpGet`, `OpCreate`, `OpUpdate`, `OpDelete`, `OpQuery`, `OpGetBatch`, `OpGetCredential`, `OpDeactivateDevice`.

### Structs
- `Request`: `Operation`, `EntityType`, `ID`, `IDs []int64`, `Payload`, `ReplyCh chan Response`.
- `Response`: `Data`, `Error`.
- `BatchDeviceResponse`: `ToPing []*Device`, `ToSkip []*Device`.

---

## `pkg/api/routes.go`

### Functions
- `RegisterEntityRoutes[T](g, path, entityType, encryptionKey, reqCh)`: Creates `GET ""`, `GET "/:id"`, `POST ""`, `PUT "/:id"`, `DELETE "/:id"` handlers.
- `RegisterMetricsRoute(g, reqCh)`: Creates `POST "/metrics"` handler.
- `maskCredentialPayload(cred)`: Sets `payload = "[HIDDEN]"`.
- `listHandler[T]`: Sends `OpList` request, decrypts results, masks credentials.
- `getHandler[T]`: Sends `OpGet` request, decrypts result, masks credentials.
- `createHandler[T]`: Encrypts entity, sends `OpCreate` request, decrypts response.
- `updateHandler[T]`: Encrypts entity, sends `OpUpdate` request, decrypts response.
- `deleteHandler`: Sends `OpDelete` request.
- `metricsHandler`: Sends `OpQuery` request with `MetricQueryRequest` payload.

---

## `pkg/api/jwtAuth.go`

### Struct `JwtAuth`
- Fields: `jwtSecret`, `adminUsername`, `adminPassHash`, `expiryHours`.

### Functions
- `Auth(cfg)`: Creates `*JwtAuth` from config.
- `LoginHandler`: Validates username, compares bcrypt hash, issues JWT with `HS256`.
- `JWTMiddleware()`: Validates `Authorization: Bearer` token.
- `SecurityHeaders()`: Sets `X-Content-Type-Options`, `X-Frame-Options`, `X-XSS-Protection`.

---

## `pkg/api/encryption.go`

### Functions
- `EncryptStruct[T](entity, secretKey)`: Uses `gocrypt` for `gocrypt:"aes"` tags, handles `json.RawMessage` fields.
- `DecryptStruct[T](entity, secretKey)`: Reverse of encrypt.
- `handleRawMessageFields(entity, secretKey, encrypt)`: Reflection-based handling for `json.RawMessage` fields.
- `DecryptPayload(cred, secretKey)`: Decrypts `CredentialProfile.Payload`, returns `json.RawMessage`. Falls back to raw JSON for unencrypted data.

---

## `pkg/Services/persistence/entityService.go`

### Struct `EntityService`
- Fields: `discoveryResultsChan`, `eventsChan`, `requestsChan` (inputs), `credentialRepo`, `deviceRepo`, `discoveryProfileRepo`, `discoveryProfileEvents`, `deviceEvents` (outputs), `deviceCache`, `credentialCache`, `cacheMu`.

### Methods
- `NewEntityService()`: Initializes repositories and caches.
- `Run(ctx)`: Selects on `ctx.Done()`, `discoveryResultsChan`, `eventsChan`, `requestsChan`.
- `provisionFromDiscovery(result)`: Checks for existing device by IP+Port. Creates device with status "discovered" or "active" based on `auto_provision`. Updates cache, publishes event.
- `handleEvent(event)`: Routes `EventTriggerDiscovery` to `triggerDiscovery`, `EventProvisionDevice` to `provisionDevice`.
- `triggerDiscovery(event)`: Fetches profile, enriches with credential, publishes `EventRunDiscovery`.
- `provisionDevice(event)`: Updates device status to "active", updates cache, publishes event.
- `handleCrudRequest(req)`: Routes by operation: `OpGetBatch`, `OpGetCredential`, `OpDeactivateDevice`, or standard CRUD by entity type.
- `handleCredentialCRUD`: Validates name, calls generic `handleCRUD`, updates cache.
- `handleDeviceCRUD`: Validates immutable fields on update, validates required fields on create, calls generic `handleCRUD`, updates cache.
- `handleDiscoveryProfileCRUD`: Enriches with credential before publishing events.
- `updateDeviceCache/updateCredentialCache`: Lock, update/delete from cache.
- `LoadCaches(ctx)`: Loads all credentials and devices into cache at startup.
- `GetActiveDeviceIDs()`: Returns IDs where `status == "active"`.
- `handleGetBatch(req)`: Reads from cache, splits by `should_ping`, skips inactive/deleted.
- `handleGetCredential(req)`: Reads from cache.
- `handleDeactivateDevice(ctx, deviceID)`: Updates status to "inactive", updates cache, publishes event.

---

## `pkg/Services/scheduling/monitorScheduler.go`

### Struct `Scheduler`
- Fields: `queue DeadlineQueue`, `entityReqChan`, `deviceEvents`, `OutputChan`, `FailureChan`, `fpingPath`, `tickInterval`, `fpingTimeout`, `fpingRetries`.

### Methods
- `NewScheduler()`: Initializes with channels and config.
- `InitQueue(deviceIDs)`: Calls `queue.InitQueue` with deadline = now.
- `Run(ctx)`: Ticker loop. Selects on `ctx.Done()`, `deviceEvents`, `ticker.C`.
- `processDeviceEvent(event)`: On Create/Update: `queue.PushEntry` with immediate deadline. Delete: lazy (ignored).
- `schedule()`: Pops expired entries via `queue.PopExpired`. Requests batch from EntityService via `OpGetBatch`. Performs `performBatchFping` on ToPing devices. Qualified devices go to `OutputChan`. Non-reachable devices emit `EventDeviceFailure` to `FailureChan`. Re-adds all to queue with new deadline.
- `performBatchFping(ips)`: Executes `fping -a -q -t <timeout> -r <retries> <ips...>`. Parses stdout for reachable IPs.

---

## `pkg/Services/scheduling/deadlinePriorityQueue.go`

### Struct `DeviceDeadline`
- Fields: `DeviceID`, `Deadline`.

### Type `DeadlineQueue`
- `[]*DeviceDeadline`, implements `heap.Interface` (min-heap by `Deadline`).

### Methods
- `Len`, `Less`, `Swap`, `Push`, `Pop`: heap.Interface.
- `Peek()`: Returns min without removing.
- `PopExpired(now)`: Pops all entries with `Deadline <= now`.
- `PushEntry(deviceID, deadline)`: `heap.Push`.
- `InitQueue(deviceIDs, now)`: Bulk initialize with same deadline.
- `PushBatch(entries)`: Append + `heap.Init` (O(n)).

---

## `pkg/Services/polling/metricsPoller.go`

### Struct `Poller`
- Fields: `pool *PluginWorkerPool`, `pluginDir`, `plugins map[string]string`, `encryptionKey`, `entityReqChan`, `InputChan`, `OutputChan`.

### Methods
- `NewPoller()`: Creates pool, loads plugins.
- `loadPlugins()`: Scans `pluginDir`, maps `pluginID -> binPath`.
- `Run(ctx)`: Starts pool, starts `collectResults` goroutine. Receives devices from `InputChan`, groups by `PluginID`, creates tasks, submits to pool.
- `groupByProtocol(devices)`: Groups `[]*Device` by `PluginID`.
- `getCredential(profileID)`: Sends `OpGetCredential` request, returns `*CredentialProfile`.
- `createTasks(devices)`: For each device, gets credential (cached locally), decrypts payload, builds `plugin.Task`.
- `collectResults(ctx)`: Forwards results from `pool.Results()` to `OutputChan`.

---

## `pkg/Services/persistence/metricsService.go`

### Types
- `jobType`: `jobTypeWrite`, `jobTypeRead`.
- `metricsJob`: `jobType`, `writeResults []plugin.Result`, `readRequest models.Request`.

### Struct `MetricsService`
- Fields: `pollResults`, `queryReqs` (inputs), `writeDB`, `readDB` (separate pools), `workerCount`, `jobChan`, `failureChan`, `defaultLimit`, `defaultRangeHours`.

### Methods
- `NewMetricsService()`: Initializes with inputs, DB pools, worker count.
- `Run(ctx)`: Starts N workers, dispatch loop selects on `ctx.Done()`, `pollResults`, `queryReqs`.
- `worker(ctx, id, wg)`: Processes jobs from `jobChan`, calls `handleWrite` or `handleQuery`.
- `handleWrite(ctx, results)`: Separates success/failure. Emits `EventDeviceFailure` for failures. Uses `pgx.CopyFrom` for batch insert.
- `handleQuery(ctx, req)`: Calls `getMetricsBatch`, sends response.
- `getMetricsBatch(ctx, deviceIDs, query)`: Validates path, applies defaults, builds SQL with JSONB path extraction, executes per device.
- `validatePath(path)`: Regex validates path for SQL injection prevention.

---

## `pkg/Services/discovery/discoveryService.go`

### Struct `discoveryContext`
- Fields: `DiscoveryProfileID`, `CredentialProfileID`, `Port`.

### Struct `DiscoveryService`
- Fields: `pool`, `events`, `resultCh`, `pluginDir`, `encryptionKey`, `pending map[string]discoveryContext`, `pendingMu`.

### Methods
- `NewDiscoveryService()`: Creates pool with `-discovery` arg.
- `Start(ctx)`: Starts pool, starts `collectResults`, main loop on `events`.
- `processEvent(ctx, event)`: On `EventRunDiscovery`: calls `runDiscovery`.
- `collectResults(ctx)`: Reads from `pool.Results()`. Clears from pending, skips failures, enriches with context, forwards success to `resultCh`.
- `runDiscovery(ctx, profile)`: Expands target, decrypts credentials, finds plugin binary, registers pending, builds tasks, submits to pool.
- `expandTarget(target)`: Routes to `expandCIDR`, `expandRange`, or single IP.
- `expandCIDR(cidr)`: Uses `net.ParseCIDR`, removes network/broadcast.
- `expandRange(rangeStr)`: Parses `start-end` or `start-lastOctet`.
- `incIP`, `copyIP`, `compareIP`: IP manipulation helpers.

---

## `pkg/Services/monitorFailure/healthMonitor.go`

### Struct `FailureRecord`
- Fields: `LastTime`, `Count`.

### Struct `FailureService`
- Fields: `failures map[int64]FailureRecord`, `failureChan`, `entityReqChan`, `window`, `threshold`.

### Methods
- `NewHealthMonitor()`: Initializes with window (minutes), threshold.
- `Run(ctx)`: Selects on `ctx.Done()`, `failureChan`. Filters for `EventDeviceFailure`, calls `handleFailure`.
- `handleFailure(event)`: If within window: increment count. If count >= threshold: call `deactivateDevice`, delete record. Else: reset count to 1.
- `deactivateDevice(deviceID)`: Sends `OpDeactivateDevice` request asynchronously.

---

## `pkg/pluginWorker/pool.go`

### Struct `PluginWorkerPool[T, R]`
- Fields: `workerCount`, `poolName`, `args`, `jobChan`, `resultChan`.

### Struct `Job[T]`
- Fields: `BinPath`, `Tasks []T`.

### Methods
- `NewPool()`: Initializes with buffer size.
- `Start(ctx)`: Spawns N workers, waits for completion in goroutine.
- `Submit(binPath, tasks)`: Sends job to `jobChan`.
- `Results()`: Returns `<-chan []R`.
- `worker(ctx, id, wg)`: Selects on `ctx.Done()`, `jobChan`. Calls `executePlugin`.
- `executePlugin(job)`: Marshals tasks to JSON, executes binary with args, captures stdout, unmarshals results.

---

## `pkg/plugin/` (implied from usage)

### Struct `Task`
- Fields: `DeviceID`, `Target`, `Port`, `Credentials json.RawMessage`.

### Struct `Result`
- Fields: `DeviceID`, `Target`, `Port`, `Success`, `Error`, `Hostname`, `Data json.RawMessage`, `DiscoveryProfileID`, `CredentialProfileID`.

---

## `plugin-code/winrm/main.go`

- Reads JSON tasks from stdin.
- If `-discovery` flag: runs `hostname` command via WinRM, returns hostname.
- Else: runs PowerShell metrics script, returns JSON data.
- Outputs JSON array of results to stdout.

---

## `scripts/hashpassword.go`

- CLI tool: takes password arg, outputs bcrypt hash.
