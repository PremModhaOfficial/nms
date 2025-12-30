# NMS Data Flow

## 1. Discovery Flow

```mermaid
sequenceDiagram
    participant API
    participant ES as EntityService
    participant DS as DiscoveryService
    participant Pool as PluginWorkerPool
    participant Plugin as winrm -discovery
    
    API->>ES: POST /discovery_profiles/:id/run
    ES->>ES: Get profile, enrich with credential
    ES->>DS: EventRunDiscovery (profile)
    DS->>DS: expandTarget() → IPs
    DS->>DS: DecryptPayload(credential)
    DS->>DS: Register pending[ip] = context
    DS->>Pool: Submit(binPath, tasks)
    Pool->>Plugin: JSON stdin
    Plugin->>Plugin: WinRM hostname
    Plugin-->>Pool: JSON stdout
    Pool-->>DS: Results
    alt Success
        DS->>DS: Enrich result with profile IDs
        DS->>ES: plugin.Result
        ES->>ES: Check existing device (IP+Port)
        ES->>ES: Determine status (auto_provision)
        ES->>DB: INSERT device
        ES->>ES: Update deviceCache
        ES->>Scheduler: EventCreate (if active)
    else Failure
        DS->>DS: Log error, delete from pending
    end
```

## 2. Polling Flow

```mermaid
graph LR
    subgraph Scheduler
        A[Ticker] --> B[queue.PopExpired]
        B --> C[OpGetBatch → EntityService]
        C --> D{should_ping?}
    end
    
    subgraph Availability Check
        D -->|ToPing| E[fping batch]
        E --> F{Reachable?}
        D -->|ToSkip| G[Skip fping]
    end
    
    subgraph Dispatch
        F -->|Yes| H[qualified list]
        G --> H
        F -->|No| I[EventDeviceFailure → HealthMonitor]
        H --> J[OutputChan → Poller]
    end
    
    subgraph Poller
        J --> K[groupByProtocol]
        K --> L[OpGetCredential × N]
        L --> M[DecryptPayload]
        M --> N[Submit to PluginWorkerPool]
    end
    
    subgraph Plugin Execution
        N --> O[winrm binary]
        O --> P[PowerShell metrics]
        P --> Q[JSON stdout]
    end
    
    subgraph Persistence
        Q --> R[MetricsService]
        R --> S{Success?}
        S -->|Yes| T[pgx.CopyFrom → DB]
        S -->|No| U[EventDeviceFailure → HealthMonitor]
    end
```

## 3. Failure Handling Flow

```mermaid
sequenceDiagram
    participant Source as Scheduler/MetricsService
    participant HM as HealthMonitor
    participant ES as EntityService
    participant DB
    
    Source->>HM: EventDeviceFailure (deviceID, timestamp, reason)
    HM->>HM: Get FailureRecord
    alt Within window
        HM->>HM: record.Count++
        alt Count >= threshold
            HM->>ES: OpDeactivateDevice
            ES->>DB: UPDATE status='inactive'
            ES->>ES: Update deviceCache
            ES->>Scheduler: EventUpdate
            HM->>HM: Delete record
        end
    else Outside window
        HM->>HM: record.Count = 1
    end
    HM->>HM: record.LastTime = timestamp
```

## 4. API Request Flow

```mermaid
sequenceDiagram
    participant Client
    participant Gin
    participant Handler
    participant ES as EntityService
    participant Repo as SqlxRepository
    participant DB
    
    Client->>Gin: HTTP Request
    Gin->>Gin: JWTMiddleware
    Gin->>Handler: Route handler
    Handler->>Handler: EncryptStruct (if create/update)
    Handler->>ES: Request{Op, EntityType, Payload, ReplyCh}
    ES->>ES: Route by EntityType
    ES->>ES: Validate (immutability, required fields)
    ES->>Repo: CRUD operation
    Repo->>DB: SQL
    DB-->>Repo: Result
    Repo-->>ES: Entity
    ES->>ES: Update cache
    ES->>Scheduler: Event (async)
    ES-->>Handler: Response{Data, Error}
    Handler->>Handler: DecryptStruct
    Handler->>Handler: maskCredentialPayload
    Handler-->>Client: JSON Response
```

## 5. Provisioning Flow

```mermaid
sequenceDiagram
    participant API
    participant ES as EntityService
    participant Scheduler
    
    API->>ES: EventProvisionDevice (deviceID, interval)
    ES->>ES: Get device from repo
    ES->>ES: Set status='active', polling_interval
    ES->>DB: UPDATE device
    ES->>ES: Update deviceCache
    ES->>Scheduler: EventUpdate
    Scheduler->>Scheduler: queue.PushEntry (immediate deadline)
```

## 6. Scheduler Tick Cycle

| Step | Action | Channel |
|------|--------|---------|
| 1 | `ticker.C` fires | - |
| 2 | `queue.PopExpired(now)` | - |
| 3 | Collect device IDs, deadline map | - |
| 4 | `OpGetBatch` request | `entityReqChan` |
| 5 | Receive `BatchDeviceResponse` | `ReplyCh` |
| 6 | Collect IPs from `ToPing` | - |
| 7 | `performBatchFping(ips)` | - |
| 8 | Filter ToPing by reachable | - |
| 9 | Emit `EventDeviceFailure` for unreachable | `FailureChan` |
| 10 | Append ToSkip to qualified | - |
| 11 | `queue.PushBatch` with new deadlines | - |
| 12 | Send qualified to Poller | `OutputChan` |

## 7. MetricsService Worker Jobs

| Job Type | Source | Handler |
|----------|--------|---------|
| `jobTypeWrite` | `pollResults` channel | `handleWrite` → `pgx.CopyFrom` |
| `jobTypeRead` | `queryReqs` channel | `handleQuery` → JSONB query |

## 8. Error Propagation

| Error Source | Handler | Effect |
|--------------|---------|--------|
| fping unreachable | Scheduler | `EventDeviceFailure(reason="ping")` |
| Plugin exit ≠ 0 | PluginWorkerPool | Empty result, logged |
| Plugin JSON parse fail | PluginWorkerPool | Empty result, logged |
| Poll result `Success=false` | MetricsService | `EventDeviceFailure(reason="poll")` |
| Threshold exceeded | HealthMonitor | `OpDeactivateDevice` → status='inactive' |
