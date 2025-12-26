# Project Progress Report: Network Management System (NMS)

**Date:** Friday, December 26, 2025  
**Project:** Network Management System (NMS) - Lite version

## Project Overview
The NMS is a lightweight, extensible network management system designed for discovery, monitoring, and metric collection from network devices. It features a plugin-based architecture for polling and a RESTful API for management.

## Key Components and Files

| File | GitHub Link | Description |
| :--- | :--- | :--- |
| `cmd/app/main.go` | [View Code](https://github.com/PremModhaOfficial/nms/blob/master/cmd/app/main.go) | Entry point of the application. It bootstraps the configuration, database, and all core services (polling, scheduling, discovery). |
| `pkg/api/routes.go` | [View Code](https://github.com/PremModhaOfficial/nms/blob/master/pkg/api/routes.go) | Centralized API routing logic using the Gin framework. It defines CRUD operations for various network entities. |
| `pkg/api/jwtAuth.go` | [View Code](https://github.com/PremModhaOfficial/nms/blob/master/pkg/api/jwtAuth.go) | Security layer implementing JWT authentication middleware to protect management endpoints. |
| `pkg/polling/metricsPoller.go` | [View Code](https://github.com/PremModhaOfficial/nms/blob/master/pkg/polling/metricsPoller.go) | Core logic for executing plugins to collect metrics from devices. Manages a worker pool for concurrent execution. |
| `pkg/scheduling/monitorScheduler.go` | [View Code](https://github.com/PremModhaOfficial/nms/blob/master/pkg/scheduling/monitorScheduler.go) | Handles the timing and scheduling of monitoring tasks, ensuring metrics are collected at defined intervals. |
| `pkg/persistence/entityService.go` | [View Code](https://github.com/PremModhaOfficial/nms/blob/master/pkg/persistence/entityService.go) | Service layer for database interactions related to network entities like devices and monitors. |
| `pkg/database/db.go` | [View Code](https://github.com/PremModhaOfficial/nms/blob/master/pkg/database/db.go) | Database initialization and repository setup using GORM. |
| `pkg/plugin/types.go` | [View Code](https://github.com/PremModhaOfficial/nms/blob/master/pkg/plugin/types.go) | Definitions for plugin interfaces, facilitating the decoupled architecture for adding new polling capabilities. |
| `schema.sql` | [View Code](https://github.com/PremModhaOfficial/nms/blob/master/schema.sql) | Full database schema definition for manual initialization. |

## Current Progress
- **Core Architecture:** Built a modular system with clear separation between API, business logic (polling/scheduling), and persistence.
- **Authentication:** Secured the API with JWT tokens.
- **Polling Engine:** Implemented a worker pool based poller that can execute external binaries as plugins.
- **Extensibility:** Successfully integrated WinRM polling as a separate plugin module.
- **Persistence:** Implemented a robust repository pattern for data management.

## Next Steps
- Implement advanced discovery logic.
- Enhance visualization of collected metrics.
- Add more polling plugins for common network protocols (SNMP, SSH).
