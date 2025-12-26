# main - cmd/server/main.go

## main
- Initializes all communication channels with specified buffer sizes.
- Connects to PostgreSQL using GORM.
- Instantiates all core services: `EntityService`, `MetricsService`, `Scheduler`, `Poller`, and `DiscoveryService`.
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
- Publishes `EventTriggerDiscovery` to `provisioningEventChan`.
- Triggers discovery for a specific profile ID.

## ProvisionDeviceHandler (provisioning.go)
- Publishes `EventProvisionDevice` to `provisioningEventChan`.
- Queues manual device provisioning with custom polling intervals.

## respondError (response.go)
- Standards API error responses in unified JSON format.
- Aborts Gin context execution.

## EncryptStruct / DecryptStruct (encryption.go)
- Uses AES-256 to encrypt/decrypt fields tagged with `gocrypt:"aes"`.

## DecryptPayload (encryption.go)
- Specialized decryptor for `CredentialProfile` JSON payloads.

## RegisterEntityRoutes (routes.go)
- Dynamically registers standard CRUD routes for generic entity types.
- Injects request channel for service layer communication.

## listHandler / getHandler / createHandler / updateHandler / deleteHandler (routes.go)
- Implements request-reply pattern over Go channels.
- Encrypts sensitive fields during create/update via `database.EncryptStruct`.
- Blocks on reply channel for synchronous API responses.

## RegisterMetricsRoute (routes.go)
- Registers `/metrics` endpoint and forwards `MetricQueryRequest` to `MetricsService` via `metricRequestChan`.

---

# database - pkg/database/

## Connect (db.go)
- Establishes GORM PostgreSQL connection using environment configuration.
- Configures connection pooling and logging.

## GormRepository (repository.go)
- Implements generic CRUD operations using GORM.
- Provides standard `List`, `Get`, `Create`, `Update`, `Delete` logic for any model.
- Includes `GetByField` for targeted entity lookups.

---

# persistence - pkg/persistence/

## EntityService (entityService.go)
- Orchestrates complex entity persistence, discovery provisioning, and event publishing.
- Maintains in-memory caches of active devices and credentials for hot-path access.

## NewEntityService (entityService.go)
- Initializes the service with necessary channels and DB connection.

## Run (entityService.go)
- Main loop consuming `discoveryResults`, `events`, and `requests`.

## provisionFromDiscovery (entityService.go)
- Atomically creates `Device` from discovery plugin output.
- Publishes events for scheduler synchronization.

## handleCrudRequest (entityService.go)
- Central router for CRUD operations across multiple entity types.
- Publishes change events to specific topics after successful DB commits.

## MetricsService (metricsService.go)
- Hot-path service for persisting high-volume polling data and handling metric queries.

## NewMetricsService (metricsService.go)
- Initializes the metrics service with raw SQL connection and default query limits.

## savePollResults (metricsService.go)
- Batches `plugin.Result` items for DB insertion.

## handleQuery (metricsService.go)
- Executes high-performance raw SQL with prepared statements for metric retrieval.

---

# discovery - pkg/discovery/

## DiscoveryService (discoveryService.go)
- Coordinates the discovery process across network ranges.
- Manages its own internal worker pool for parallel scanning.

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

## Poller (poller.go)
- Manages the execution of protocol-specific plugins for metrics collection.
- Groups tasks by protocol to optimize worker pool utilization.

## Run (poller.go)
- Consumes batches of `*models.Device` from the scheduler.
- Groups devices by `PluginID` for batched execution.
- Decrypts credentials and submits tasks to `PollPool`.

## collectResults (poller.go)
- Proxies poll results from worker pool to `MetricsService` via `pollResultChan`.

---

# scheduler - pkg/scheduler/

## Scheduler (scheduler.go)
- Manages the polling schedule using a min-heap priority queue (deadlines).
- Decouples scheduling logic from network reachability via `fping` batches.

## LoadCache (scheduler.go)
- Hydrates in-memory map of devices with calculated deadlines.

## Run (scheduler.go)
- Ticks every interval to identify due devices.
- Processes `deviceChan` and `credentialChan` events to maintain cache consistency.

## schedule (scheduler.go)
- Identifies due devices and performs batch `fping` reachability check.
- Updates deadlines based on `PollingIntervalSeconds`.

---

# worker - pkg/worker/

## Pool (pool.go)
- Generic worker pool implementation for external binary execution.
- Handles stdin/stdout JSON marshalling for plugin communication.

## NewPool (pool.go)
- Creates a new pool with fixed worker count and named identifying tag.

## Start (pool.go)
- Spawns fixed number of worker goroutines.

## executePlugin (pool.go)
- Marshals tasks to JSON and pipes to plugin via `stdin`.
- Unmarshals plugin results from `stdout`.
- Captures `stderr` for structured logging.

---

# config - pkg/config/

## Config (config.go)
- Central structure for application configuration.
- Mapped from `app.yaml` and environment variables via Viper tags.

## LoadConfig (config.go)
- Reads `app.yaml` and environment variables via `viper`.

---

# models - pkg/models/

## Device (models.go)
- Core entity representing a monitored node (IP, Port, Plugin, Credentials).

## CredentialProfile (models.go)
- Encrypted storage for protocol-specific credentials (SNMP, WinRM, etc).

## DiscoveryProfile (models.go)
- Definition for network scanning (CIDR/Range, Port, Protocol).

## Metric (models.go)
- Database schema for raw JSONB metric storage.

## Event (event.go)
- Wrapper for internal system messages (Create/Update/Delete/Trigger).

## Request / Response (request.go)
- Pair for synchronous request-reply communication across channels.

## TableName (models.go)
- Explicit GORM table name overrides for all domain entities.

## GetID (models.go)
- Satisfies `Identifiable` interface for generic repository use.

---

# plugin - pkg/plugin/

## Task (types.go)
- JSON contract for data sent TO a plugin (Target, Port, Credentials).

## Result (types.go)
- JSON contract for data returned FROM a plugin (Metrics, Success/Error).
