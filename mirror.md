# NMS Master Project Mirror

This document is a hand-crafted, comprehensive summary of every file in the NMS project. It describes the purpose, logic, and execution flow of each component to provide a complete understanding of the system without needing to read the source code.

---

## 📂 Project Root (Infrastructure & Orchestration)

### `Makefile`
- **Purpose**: Task runner for build, run, and maintenance operations.
- **Targets**:
    - `build`: Compiles the server (`nms-server`) and the WinRM plugin.
    - `run`: Executes `start.sh`.
    - `seed`: Runs `seed.py` to populate initial data.
    - `db-setup`: Initializes the PostgreSQL database using `schema.sql`.
    - `first-run`: Sequential execution of `stop`, `db-setup`, `build`, and `run`.

### `schema.sql`
- **Purpose**: Defines the PostgreSQL database schema.
- **Tables**:
    - `credential_profiles`: Stores encrypted credentials (AES) and protocol types.
    - `discovery_profiles`: Stores scan targets (IP/CIDR) and scheduling details.
    - `devices`: Primary entity table for managed network devices.
    - `metrics`: Time-series table for raw polling data (JSONB).

### `start.sh`
- **Purpose**: Secure server startup script.
- **Flow**:
    1. Compiles server and WinRM plugin into `bin/`.
    2. Sets up environment variables for database connection.
    3. **Security**: Generates bcrypt hashes for custom admin passwords using `scripts/hashpassword.go`.
    4. **Secrets**: Generates random JWT secrets if not provided.
    5. Launches `bin/nms-app`.

### `app.yaml`
- **Purpose**: Default configuration values for the application (Viper-compatible).
- **Contents**: Database defaults, worker counts, polling intervals, and resource limits.

### `seed.sh` / `seed.py`
- **Purpose**: Database seeding for demonstration/testing.
- **Flow**:
    1. Waits for server to be reachable.
    2. Logs in to obtain a JWT.
    3. Creates a Credential Profile.
    4. Creates an Auto-Provisioning Discovery Profile to trigger initial device detection.

---

## 📂 Command Layer (`cmd/`)

### `cmd/app/main.go`
- **Purpose**: System entry point and lifecycle manager.
- **Flow**: `InitLogger` -> `LoadConfig` -> `InitDB` -> `InitServices` -> `LoadCaches` -> `StartServices` -> `StartHTTPServer`.
- **Logic**: Uses `signal.NotifyContext` for graceful shutdown, ensuring all background goroutines stop cleanly.

---

## 📂 API Layer (`pkg/api/`)

### `routes.go`
- **Purpose**: Defines RESTful endpoints and generic CRUD handlers.
- **Flow**: 
    - `RegisterEntityRoutes`: Generic wrapper for GET, POST, PUT, DELETE.
    - `listHandler`: Sends `OpList` to `EntityService`, decrypts results, masks credentials, and returns JSON.

### `jwtAuth.go`
- **Purpose**: Middleware and handlers for security.
- **Logic**: `LoginHandler` validates credentials against bcrypt hashes and issues signed JWTs. `JWTMiddleware` validates the `Authorization: Bearer` header.

### `encryption.go`
- **Purpose**: AES encryption/decryption layer for struct fields tagged with `gocrypt`.
- **Logic**: Handles standard strings and `json.RawMessage` fields (like credential payloads).

---

## 📂 Core Services (`pkg/Services/`)

### `persistence/entityService.go`
- **Purpose**: The "Source of Truth" for devices and credentials.
- **Logic**:
    - Maintains in-memory caches (`deviceCache`, `credentialCache`) protected by RWMutex.
    - `handleCrudRequest`: Routes incoming requests (from API/Scheduler) to repo operations and updates caches.
    - **Calls**: `database.Repository` methods, broadcasts `models.Event` to other services.

### `scheduling/monitorScheduler.go`
- **Purpose**: Time-based polling triggers.
- **Logic**:
    - Uses `DeadlineQueue` (Min-Heap) to track when devices are due for polling.
    - `schedule()`: Pops expired items -> requests data from `EntityService` -> runs `fping` -> dispatches to Poller -> pushes back to queue with new deadline.
- **Calls**: `EntityService` (request-reply), `fping` (os/exec), `Poller` (channel).

### `polling/metricsPoller.go`
- **Purpose**: Executes plugins for data collection.
- **Logic**:
    - Groups devices by protocol.
    - Fetches and decrypts credentials for each batch.
    - Submits jobs to `pluginWorker.Pool`.
- **Calls**: `PluginWorkerPool.Submit`, `api.DecryptPayload`.

### `persistence/metricsWriter.go`
- **Purpose**: High-performance sink for metrics.
- **Logic**: Uses `pgx.CopyFrom` for bulk insert of JSON results into the `metrics` table.
- **Calls**: `pgx` driver.

### `persistence/metricsReader.go`
- **Purpose**: API interface for time-series data.
- **Logic**: Supports JSONB path filtering (e.g., `cpu.total`) with SQL injection protection via regex path validation.

### `discovery/discoveryService.go`
- **Purpose**: Active network discovery.
- **Logic**: Expands CIDRs/ranges into IPs -> runs discovery plugin -> forwards results to `EntityService`.
- **Calls**: `expandTarget` (IP logic), `PluginWorkerPool`.

### `monitorFailure/healthMonitor.go`
- **Purpose**: Automatic device deactivation.
- **Logic**: Monitors failure counts within a sliding window. If threshold is hit, it deactivates the device via `EntityService`.

---

## 📂 Plugins & Workers (`pkg/plugin/`, `pkg/pluginWorker/`)

### `pluginWorker/pool.go`
- **Purpose**: Generic batch execution of external binaries.
- **Logic**: Spawns N workers that consume `Job{BinPath, Tasks}`. Communicates with binaries via JSON Over Stdin/Stdout.

### `plugin-code/winrm/main.go`
- **Purpose**: Implementation of the WinRM polling/discovery plugin.
- **Logic**:
    1. Reads batch of tasks from Stdin.
    2. Concurrently (goroutines) connects to Windows hosts.
    3. Runs `hostname` for discovery or a massive PowerShell metrics script for polling.
    4. Outputs JSON array of results to Stdout.

---

## 📂 Data Models & Communication (`pkg/models/`)

### `models.go` / `event.go` / `request.go`
- **Purpose**: Shared structures for internal and external communication.
- **Logic**:
    - `Event`: Asynchronous notification (e.g., "DeviceCreated").
    - `Request`: Synchronous request-reply pattern (API -> EntityService).
    - `Device/CredentialProfile`: Database/JSON entities.

---

## 📂 Scripts & Utilities (`scripts/`)

### `scripts/hashpassword.go`
- **Purpose**: CLI tool to generate bcrypt hashes (used by `start.sh`).

---

## 📂 Quality Assurance (`tests/`)

### `tests/api/`
- **Purpose**: Integration tests for API validation and business logic.
- **Highlights**: `setup_test.go` creates a mock harness with channels to verify that API requests correctly trigger backend events.
