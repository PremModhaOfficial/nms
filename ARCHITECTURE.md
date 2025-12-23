# NMS Architecture Document

This document provides a comprehensive architecture overview of the NMS (Network Management System) codebase, covering data flows, event flows, component decoupling, and design decisions.

## Table of Contents

1. [System Overview](#system-overview)
2. [Component Architecture](#component-architecture)
3. [Data Flow](#data-flow)
4. [Event Flow](#event-flow)
5. [Decoupling Patterns](#decoupling-patterns)
6. [Technology Choices & Reasoning](#technology-choices--reasoning)
7. [Package Reference](#package-reference)

---

## System Overview

NMS is a **single-binary Network Monitoring System** built in Go, designed for discovering and polling network devices. The architecture follows a strict **Event-Driven Architecture (EDA)** pattern using Go channels as the internal message bus.

```mermaid
graph TB
    subgraph "API Layer"
        API[Gin HTTP Server]
    end

    subgraph "Communication Bus"
        CH[Go Channels]
    end

    subgraph "Service Layer"
        SCH[Scheduler]
        POL[Poller]
        DISC[DiscoveryService]
    end

    subgraph "Data Layer"
        EW[EntityWriter]
        MW[MetricsWriter]
    end

    subgraph "Storage"
        PG[(PostgreSQL)]
    end

    subgraph "External"
        PLUGINS[Plugin Binaries]
        FPING[fping]
    end

    API --> CH
    CH --> SCH
    CH --> DISC
    CH --> EW
    CH --> MW

    SCH --> POL
    POL --> CH
    DISC --> CH

    POL --> PLUGINS
    DISC --> PLUGINS
    SCH --> FPING

    EW --> PG
    MW --> PG
```

### Key Architectural Properties

| Property | Value | Reasoning |
|----------|-------|-----------|
| **Deployment Model** | Single binary | Zero external dependencies, simple ops |
| **Message Queue** | Go channels | Type-safe, zero latency, no external deps |
| **API Framework** | Gin | High performance, minimal overhead |
| **ORM** | GORM | Familiar, powerful query builder |
| **Plugin Model** | JSON over stdin/stdout | Language-agnostic, simple IPC |

---

## Component Architecture

### Layer Diagram

```mermaid
graph LR
    subgraph "Presentation"
        A1[API Handlers]
        A2[Auth Middleware]
    end

    subgraph "Business Logic"
        B1[Scheduler]
        B2[Poller]
        B3[DiscoveryService]
    end

    subgraph "Data Access"
        C1[EntityWriter]
        C2[MetricsWriter]
        C3[Repositories]
    end

    A1 --> B1
    A1 --> B2
    A1 --> B3
    B1 --> C1
    B2 --> C2
    B3 --> C1
```

### Package Structure

```
pkg/
├── api/              # HTTP handlers (thin, stateless)
├── database/         # Repository implementations, DB connection
├── datawriter/       # Central data persistence service
├── discovery/        # Device discovery orchestration
├── models/           # Domain entities & events
├── plugin/           # Plugin contract types
├── poller/           # Polling orchestration
├── scheduler/        # Timer-based poll scheduling
└── worker/           # Generic worker pool
```

---

## Data Flow

### 1. API Request → Database Flow

The API layer is **completely decoupled** from the data layer. All CRUD operations flow through a **request-reply pattern** over channels.

```mermaid
sequenceDiagram
    participant Client
    participant API as API Handler
    participant CH as crudRequestChan
    participant EW as EntityWriter
    participant DB as PostgreSQL

    Client->>API: POST /api/v1/monitors
    API->>API: Validate & encrypt
    API->>CH: Request{Op: create, Payload: monitor, ReplyCh}
    activate EW
    EW->>DB: INSERT INTO monitors
    DB-->>EW: OK
    EW->>CH: Response{Data: monitor}
    deactivate EW
    API-->>Client: 201 Created
```

**Why this pattern?**
- API handlers have **zero repository dependencies**
- All DB access is serialized through `EntityWriter` (single writer)
- Easy to add cross-cutting concerns (logging, validation) in one place
- Enables future distributed architectures (swap channel for message queue)

### 2. Polling Data Flow

```mermaid
sequenceDiagram
    participant SCH as Scheduler
    participant FPING as fping
    participant POL as Poller
    participant POOL as Worker Pool
    participant PLUGIN as Plugin Binary
    participant MW as MetricsWriter
    participant DB as PostgreSQL

    SCH->>SCH: Check monitor deadlines
    SCH->>FPING: Batch ping IPs
    FPING-->>SCH: Reachable IPs
    SCH->>POL: []*Monitor via schedulerToPollerChan
    POL->>POOL: Submit tasks by plugin_id
    POOL->>PLUGIN: JSON tasks via stdin
    PLUGIN-->>POOL: JSON results via stdout
    POOL->>POL: []plugin.Result
    POL->>MW: []plugin.Result via pollResultChan
    MW->>DB: INSERT INTO metrics
```

**Key Design Decisions:**
- **Batch fping**: Single system call for all candidate IPs (O(1) instead of O(n))
- **Plugin grouping**: Tasks are grouped by `plugin_id` to batch protocol-specific operations
- **Hierarchical JSON**: Raw plugin output stored as JSONB for flexible querying

### 3. Discovery Flow

```mermaid
sequenceDiagram
    participant API as API Handler
    participant CMD as provisioningCommandChan
    participant EW as EntityWriter
    participant DISC as DiscoveryService
    participant POOL as Worker Pool
    participant PLUGIN as Plugin Binary
    participant DB as PostgreSQL

    API->>CMD: EventTriggerDiscovery{profile_id}
    EW->>EW: Fetch DiscoveryProfile
    EW->>DISC: Event{type: update, payload: profile}
    
    DISC->>DISC: Expand CIDR to IPs
    DISC->>POOL: Submit discovery tasks
    POOL->>PLUGIN: JSON tasks + "-discovery" flag
    PLUGIN-->>POOL: JSON results with hostname
    POOL->>DISC: []plugin.Result
    DISC->>EW: plugin.Result via discResultChan
    EW->>DB: INSERT device + monitor
```

---

## Event Flow

The system uses a **topic-based channel architecture**. Each channel represents a specific event topic.

### Channel Topology

```mermaid
graph TD
    subgraph "Event Channels (models.Event)"
        MC[monitorChan]
        CC[credentialChan]
        DPC[discProfileChan]
        PROV[provisioningCommandChan]
    end

    subgraph "Data Channels"
        PRC[pollResultChan - []plugin.Result]
        DRC[discResultChan - plugin.Result]
        S2P[schedulerToPollerChan - []*Monitor]
    end

    subgraph "Request-Reply Channels"
        CRUD[crudRequestChan]
        METRIC[metricRequestChan]
    end

    API --> CRUD
    API --> METRIC
    API --> PROV

    EW --> MC
    EW --> CC
    EW --> DPC

    MC --> SCH
    CC --> SCH
    DPC --> DISC

    S2P --> POL
    POL --> PRC
    DISC --> DRC

    PRC --> MW
    DRC --> EW
    PROV --> EW
```

### Event Types

| Event Type | Channel | Producer | Consumer | Purpose |
|------------|---------|----------|----------|---------|
| `create/update/delete` | monitorChan | EntityWriter | Scheduler | Update in-memory cache |
| `create/update/delete` | credentialChan | EntityWriter | Scheduler | Update credential cache |
| `create/update/delete` | discProfileChan | EntityWriter | DiscoveryService | Trigger discovery runs |
| `trigger_discovery` | provisioningCommandChan | API | EntityWriter | Manual discovery trigger |
| `provision_device` | provisioningCommandChan | API | EntityWriter | Manual device provisioning |
| `[]plugin.Result` | pollResultChan | Poller | MetricsWriter | Persist metrics |
| `plugin.Result` | discResultChan | DiscoveryService | EntityWriter | Provision from discovery |
| `[]*models.Monitor` | schedulerToPollerChan | Scheduler | Poller | Dispatch poll tasks |

### Event Publishing Pattern

Events are published **after successful DB operations** using a non-blocking send:

```go
func sendEvent(ch chan<- models.Event, event models.Event) {
    select {
    case ch <- event:
    default:
        slog.Warn("Channel full, dropping event")
    }
}
```

**Why non-blocking?**
- Prevents slow consumers from blocking the EntityWriter
- Drops events gracefully under backpressure
- Consumers maintain their own caches anyway

---

## Decoupling Patterns

### 1. API → Service Layer Decoupling

The API layer has **zero direct dependencies** on repositories or services:

```mermaid
graph LR
    subgraph "API Package"
        H[Handler Function]
    end

    subgraph "Injection"
        CH["chan<- models.Request"]
    end

    subgraph "Service Layer"
        EW[EntityWriter]
    end

    H -->|"sends Request"| CH
    CH -->|"receives"| EW
    EW -->|"sends Response"| H
```

**Implementation:**
```go
// API handler - only knows about the channel
func listHandler(entityType string, reqCh chan<- models.Request) gin.HandlerFunc {
    return func(c *gin.Context) {
        replyCh := make(chan models.Response, 1)
        reqCh <- models.Request{
            Operation:  models.OpList,
            EntityType: entityType,
            ReplyCh:    replyCh,
        }
        resp := <-replyCh  // Sync wait
        // ...
    }
}
```

### 2. Service → Data Layer Decoupling

Services depend on the **Repository interface**, not implementations:

```go
type Repository[T any] interface {
    List(ctx context.Context) ([]*T, error)
    Get(ctx context.Context, id int64) (*T, error)
    Create(ctx context.Context, entity *T) (*T, error)
    Update(ctx context.Context, id int64, entity *T) (*T, error)
    Delete(ctx context.Context, id int64) error
}
```

### 3. Plugin Contract Decoupling

Plugins are **external binaries** with a strict JSON contract:

```mermaid
graph LR
    subgraph "Core"
        P[Poller/Discovery]
    end

    subgraph "Plugin Binary"
        PLUGIN[Any Language]
    end

    P -->|"JSON []Task via stdin"| PLUGIN
    PLUGIN -->|"JSON []Result via stdout"| P
```

**Task Input:**
```json
[{"target": "192.168.1.10", "port": 5985, "credentials": "{...}"}]
```

**Result Output:**
```json
[{"target": "192.168.1.10", "success": true, "data": {...}}]
```

### 4. Scheduler Cache Pattern

The Scheduler maintains an **in-memory cache** synchronized via events:

```mermaid
graph TD
    DB[(PostgreSQL)]
    EW[EntityWriter]
    CH[monitorChan]
    SCH[Scheduler Cache]

    DB -->|"initial load"| SCH
    EW -->|"Event{Create/Update/Delete}"| CH
    CH -->|"sync"| SCH
```

**Why a cache?**
- Sub-millisecond deadline checking (no DB round-trips)
- Scheduler runs every 5 seconds - would overload DB otherwise
- Event-driven sync keeps cache consistent

---

## Technology Choices & Reasoning

### Go Channels as Message Bus

```go
monitorChan := make(chan models.Event, 100)
```

| Consideration | Choice | Reasoning |
|---------------|--------|-----------|
| **Type Safety** | ✅ Channels | Compile-time type checking, no runtime serialization errors |
| **Latency** | ✅ Channels | Zero network hop, in-process communication |
| **Dependencies** | ✅ Channels | No Redis/RabbitMQ to deploy and monitor |
| **Persistence** | ❌ Channels | Messages lost on crash - acceptable for monitoring |
| **Scalability** | ❌ Channels | Single-node only - acceptable for target use case |

### GORM + Raw SQL Hybrid

| Use Case | Approach | Reasoning |
|----------|----------|-----------|
| **CRUD Operations** | GORM | Simple, type-safe, handles migrations |
| **Metrics Queries** | Raw SQL | JSONB path queries, prepared statements for performance |

```go
// GORM for CRUD
result := r.db.WithContext(ctx).Create(entity)

// Raw SQL for complex queries
sqlQuery := `SELECT timestamp, data #> '{cpu,total}' FROM metrics WHERE ...`
```

### Generic Repository Pattern

Go 1.18+ generics enable a **single repository implementation** for all entity types:

```go
type GormRepository[T any] struct {
    db *gorm.DB
}

func (r *GormRepository[T]) Create(ctx context.Context, entity *T) (*T, error) {
    result := r.db.WithContext(ctx).Create(entity)
    return entity, result.Error
}
```

**Benefits:**
- Zero code duplication across entities
- Consistent behavior (logging, error handling)
- Easy to add cross-cutting concerns

### Plugin Execution Model

```mermaid
graph LR
    P[Pool] -->|"exec.Command"| BIN[Plugin Binary]
    BIN -->|"stdin"| JSON1[JSON Tasks]
    BIN -->|"stdout"| JSON2[JSON Results]
    BIN -->|"stderr"| LOG[Logs]
```

| Alternative | Rejected Because |
|-------------|------------------|
| **gRPC** | Requires code generation, more complex |
| **HTTP Server** | Port management, lifecycle complexity |
| **Go Plugins** | Must be Go, CGO issues, version coupling |
| **Shared Memory** | Platform-specific, complex |

---

## Package Reference

### [cmd/server/main.go](file:///home/prem-modha/projects/nms/cmd/server/main.go)
Application entry point. Initializes all channels, repositories, and services. Single goroutine orchestration.

### [pkg/api/](file:///home/prem-modha/projects/nms/pkg/api)
- **routes.go**: Generic CRUD route registration using Go generics
- **auth.go**: JWT authentication middleware
- **provisioning.go**: Manual discovery/provisioning command handlers

### [pkg/database/](file:///home/prem-modha/projects/nms/pkg/database)
- **repository.go**: Generic `Repository[T]` interface and GORM implementation
- **metricsRepository.go**: Specialized JSONB queries for time-series metrics
- **encryption.go**: AES encryption for credential payloads

### [pkg/datawriter/](file:///home/prem-modha/projects/nms/pkg/datawriter)
- **entityWriter.go**: Central CRUD service, discovery provisioning, event publishing
- **metricsWriter.go**: High-volume metrics persistence

### [pkg/scheduler/scheduler.go](file:///home/prem-modha/projects/nms/pkg/scheduler/scheduler.go)
Timer-based monitor scheduling with in-memory cache. Batch fping for reachability checks.

### [pkg/poller/poller.go](file:///home/prem-modha/projects/nms/pkg/poller/poller.go)
Polling orchestration. Groups monitors by plugin, submits to worker pool.

### [pkg/discovery/discoveryService.go](file:///home/prem-modha/projects/nms/pkg/discovery/discoveryService.go)
Discovery profile processing. CIDR expansion, plugin execution, result enrichment.

### [pkg/worker/pool.go](file:///home/prem-modha/projects/nms/pkg/worker/pool.go)
Generic worker pool for plugin execution. Handles stdin/stdout JSON communication.

### [pkg/models/](file:///home/prem-modha/projects/nms/pkg/models)
- **models.go**: Domain entities (Monitor, Device, CredentialProfile, etc.)
- **event.go**: Event types for inter-service communication
- **request.go**: Request/Response types for synchronous channel communication

### [pkg/plugin/types.go](file:///home/prem-modha/projects/nms/pkg/plugin/types.go)
Plugin contract: `Task` (input) and `Result` (output) types.

---

## Summary

The NMS codebase demonstrates a **clean, Go-idiomatic architecture** with the following strengths:

1. **Full Decoupling**: API → Service → Data layers communicate only via channels and interfaces
2. **Event-Driven**: CRUD operations trigger events that sync caches and initiate workflows
3. **Generic Patterns**: Go generics eliminate boilerplate for repositories and handlers
4. **Plugin Isolation**: External binaries with JSON contracts enable language-agnostic extensions
5. **Single Binary**: No external message queues or caches - simple deployment

**Trade-offs Accepted:**
- In-flight messages lost on crash (acceptable for monitoring)
- Single-node only (target use case)
- Plugin execution has subprocess overhead (acceptable for polling intervals)
