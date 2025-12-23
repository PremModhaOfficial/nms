# NMS Code Mirror Map

Declarative map of the NMS codebase functions and their technical roles.

---

# main - cmd/server/main.go

## main
- Initializes all communication channels with specified buffer sizes.
- Connects to PostgreSQL using GORM.
- Instantiates all core services: `EntityWriter`, `MetricsWriter`, `Scheduler`, `Poller`, and `DiscoveryService`.
- Starts services as background goroutines.
- Configures and starts Gin HTTP server with API routes and JWT middleware.

---

# api - pkg/api/

## LoginHandler (auth.go)
- Validates admin credentials against environment variables.
- Issues JWT tokens with 24-hour expiration.

## JWTMiddleware (auth.go)
- Validates `Authorization: Bearer <token>` header.
- Injects claims into Gin context.

## RunDiscoveryHandler (provisioning.go)
- Publishes `EventTriggerDiscovery` to `provisioningCommandChan`.
- Triggers discovery for a specific profile ID.

## ProvisionDeviceHandler (provisioning.go)
- Publishes `EventProvisionDevice` to `provisioningCommandChan`.
- Queues manual device provisioning with custom polling intervals.

## respondError (response.go)
- Standards API error responses in unified JSON format.
- Aborts Gin context execution.

## RegisterEntityRoutes (routes.go)
- Dynamically registers standard CRUD routes for generic entity types.
- Injects request channel for service layer communication.

## listHandler / getHandler / createHandler / updateHandler / deleteHandler (routes.go)
- Implements request-reply pattern over Go channels.
- Encrypts sensitive fields during create/update via `database.EncryptStruct`.
- Blocks on reply channel for synchronous API responses.

## metricsHandler (routes.go)
- Parses `BatchMetricQuery` and forwards to `MetricsWriter` via `metricRequestChan`.

---

# database - pkg/database/

## Connect (db.go)
- Establishes GORM PostgreSQL connection using environment configuration.
- Configures connection pooling and logging.

## EncryptStruct / DecryptStruct (encryption.go)
- Uses AES-256 to encrypt/decrypt fields tagged with `gocrypt:"aes"`.

## DecryptPayload (encryption.go)
- Specialized decryptor for `CredentialProfile` JSON payloads.

## NewMetricRepository (metricsRepository.go)
- Initializes repository for high-performance raw SQL metric queries.

## GetMetricsBatch (metricsRepository.go)
- Executes parameterized raw SQL with prepared statements.
- Performs dot-notation to JSONB path conversion (e.g., `cpu.total` -> `{cpu,total}`).
- Validates paths against regex to prevent SQL injection.

## GormRepository (repository.go)
- Implements generic CRUD operations using GORM.
- Provides standard `List`, `Get`, `Create`, `Update`, `Delete` logic for any model.

## PreloadingDiscoveryProfileRepo (preloadingRepository.go)
- Decorates `DiscoveryProfile` repository with automatic `CredentialProfile` preloading.

---

# datawriter - pkg/datawriter/

## NewEntityWriter (entityWriter.go)
- Orchestrates complex entity persistence and event publishing.

## Run (entityWriter.go)
- Main loop consuming `discoveryResults`, `commands`, and `requests`.

## provisionFromDiscovery (entityWriter.go)
- Atomically creates `Device` and `Monitor` from discovery plugin output.
- Publishes `monitorChan` events for scheduler synchronization.

## handleCRUD (entityWriter.go)
- Generic internal handler for processing `models.Request` items.
- Publishes change events to specific topics after successful DB commits.

## NewMetricsWriter (metricsWriter.go)
- Hot-path service for persisting high-volume polling data.

## savePollResults (metricsWriter.go)
- Batches `plugin.Result` items into `models.Metric` records for DB insertion.

---

# discovery - pkg/discovery/

## Start (discoveryService.go)
- Initializes discovery worker pool and result collector.
- Listens for `DiscoveryProfile` changes to trigger new runs.

## runDiscovery (discoveryService.go)
- Expands target CIDR/ranges into individual IPs.
- Path-finds protocol plugins and submits tasks to `worker.Pool`.

## expandTarget / expandCIDR / expandRange (discoveryService.go)
- Utility functions for IP address mathematics and target expansion.

---

# poller - pkg/poller/

## Run (poller.go)
- Consumes batches of `*models.Monitor` from the scheduler.
- Groups monitors by `PluginID` for batched execution.
- Decrypts credentials and submits tasks to `PollPool`.

## collectResults (poller.go)
- Proxies poll results from worker pool to `MetricsWriter` via `pollResultChan`.

---

# scheduler - pkg/scheduler/

## LoadCache (scheduler.go)
- Hydrates in-memory map of monitors with calculated deadlines.

## Run (scheduler.go)
- Ticks every interval to identify due monitors.
- Processes `monitorChan` and `credentialChan` events to maintain cache consistency.

## schedule (scheduler.go)
- Identifies due monitors and performs batch `fping` reachability check.
- Updates deadlines based on `PollingIntervalSeconds`.

---

# worker - pkg/worker/

## NewPool (pool.go)
- Generic worker pool implementation for external binary execution.

## Start (pool.go)
- Spawns fixed number of worker goroutines.

## executePlugin (pool.go)
- Marshals tasks to JSON and pipes to plugin via `stdin`.
- Unmarshals plugin results from `stdout`.
- Captures `stderr` for structured logging.

---

# models - pkg/models/

## LoadConfig (config.go)
- Reads `app.yaml` and environment variables via `viper`.

## TableName (models.go)
- Explicit GORM table name overrides for all domain entities.

## GetID (models.go)
- Satisfies `Identifiable` interface for generic repository use.

---

# plugin - pkg/plugin/

## Task / Result (types.go)
- Defines the JSON contract for all external plugins.
