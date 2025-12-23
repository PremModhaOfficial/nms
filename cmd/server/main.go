package main

import (
	"context"
	"log/slog"
	"os"

	"nms/pkg/api"
	"nms/pkg/database"
	"nms/pkg/discovery"
	"nms/pkg/models"
	"nms/pkg/persistence"
	"nms/pkg/plugin"
	"nms/pkg/poller"
	"nms/pkg/scheduler"

	"github.com/gin-gonic/gin"
)

func main() {
	// ══════════════════════════════════════════════════════════════
	// STRUCTURED LOGGING
	// ══════════════════════════════════════════════════════════════
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// ══════════════════════════════════════════════════════════════
	// CONFIGURATION
	// ══════════════════════════════════════════════════════════════
	config, err := models.LoadConfig(".")
	if err != nil {
		slog.Warn("Failed to load config, using defaults", "error", err)
		config.PluginsDir = "plugins"
		config.FpingPath = "/usr/bin/fping"
		config.SchedulerTickIntervalSeconds = 5
		config.FpingTimeoutMs = 500
		config.FpingRetryCount = 2
		config.PollingWorkerConcurrency = 5
		config.DiscoveryWorkerConcurrency = 3
		config.JWTSecret = "default-insecure-secret-change-me"
	}
	slog.Info("Config loaded", "fping_path", config.FpingPath, "tick_interval", config.SchedulerTickIntervalSeconds)

	// Set JWT secret for the api package
	api.SetJWTSecret(config.JWTSecret)

	// ══════════════════════════════════════════════════════════════
	// DATABASE
	// ══════════════════════════════════════════════════════════════
	db, err := database.Connect()
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}

	// ══════════════════════════════════════════════════════════════
	// COMMUNICATION CHANNELS - One per topic
	// ══════════════════════════════════════════════════════════════
	// Queue Choice Justification:
	// We use Go channels as the internal message queue. This provides:
	// 1. Zero external dependencies (no Redis, RabbitMQ, etc.)
	// 2. Type-safe message passing with compile-time checks.
	// 3. Excellent performance for single-binary deployments.
	// 4. Simple semantics (send/receive) that are idiomatic to Go.
	// Trade-off: Messages are not persisted; a crash loses in-flight events.
	// For production-critical scenarios, consider an external queue.
	monitorChan := make(chan models.Event, 100)
	credentialChan := make(chan models.Event, 100)
	discProfileChan := make(chan models.Event, 100)
	pollResultChan := make(chan []plugin.Result, 100)
	discResultChan := make(chan plugin.Result, 100)
	schedulerToPollerChan := make(chan []*models.Monitor, 10)
	provisioningChan := make(chan models.Event, 100)

	// New request-reply channels for CRUD and metrics
	crudRequestChan := make(chan models.Request, 100)
	metricRequestChan := make(chan models.Request, 100)

	// ══════════════════════════════════════════════════════════════
	// REPOSITORIES - All owned by DataWriter (service layer)
	// ══════════════════════════════════════════════════════════════
	credRepo := database.NewGormRepository[models.CredentialProfile](db)
	monRepo := database.NewGormRepository[models.Monitor](db)
	discProfileRepo := database.NewGormRepository[models.DiscoveryProfile](db)
	deviceRepo := database.NewGormRepository[models.Device](db)

	// Get raw sql.DB for MetricRepository (uses prepared statements directly)
	sqlDB, err := db.DB()
	if err != nil {
		slog.Error("Failed to get sql.DB from GORM", "error", err)
		os.Exit(1)
	}
	metricRepo, err := database.NewMetricRepository(sqlDB)
	if err != nil {
		slog.Error("Failed to create MetricRepository", "error", err)
		os.Exit(1)
	}

	// ══════════════════════════════════════════════════════════════
	// SERVICES
	// ══════════════════════════════════════════════════════════════
	sched := scheduler.NewScheduler(
		monitorChan,
		credentialChan,
		schedulerToPollerChan,
		config.FpingPath,
		config.SchedulerTickIntervalSeconds,
		config.FpingTimeoutMs,
		config.FpingRetryCount,
	)

	poll := poller.NewPoller(
		config.PluginsDir,
		config.PollingWorkerConcurrency,
		schedulerToPollerChan,
		pollResultChan,
	)

	discService := discovery.NewDiscoveryService(
		discProfileChan,
		discResultChan,
		config.PluginsDir,
		config.DiscoveryWorkerConcurrency,
	)

	// MetricsService handles high-volume poll results and metric queries
	metricsWriter := persistence.NewMetricsService(
		pollResultChan,
		metricRequestChan,
		metricRepo,
		db,
	)

	// EntityService handles CRUD, discovery provisioning, and commands
	entityWriter := persistence.NewEntityService(
		discResultChan,
		provisioningChan,
		crudRequestChan,
		db,
		credRepo,
		monRepo,
		deviceRepo,
		discProfileRepo,
		discProfileChan,
		monitorChan,
		credentialChan,
	)

	// ══════════════════════════════════════════════════════════════
	// INITIAL CACHE LOAD
	// ══════════════════════════════════════════════════════════════
	ctx := context.Background()
	initialMonitors, err := monRepo.List(ctx)
	if err != nil {
		slog.Error("Failed to list monitors for scheduler", "error", err)
	}
	initialCreds, err := credRepo.List(ctx)
	if err != nil {
		slog.Error("Failed to list credentials for scheduler", "error", err)
	}
	sched.LoadCache(initialMonitors, initialCreds)
	slog.Info("Scheduler cache loaded from database")

	// ══════════════════════════════════════════════════════════════
	// START SERVICES
	// ══════════════════════════════════════════════════════════════
	go sched.Run(ctx)
	go poll.Run(ctx)
	go discService.Start(ctx)
	go metricsWriter.Run(ctx)
	go entityWriter.Run(ctx)

	// ══════════════════════════════════════════════════════════════
	// ROUTER SETUP
	// ══════════════════════════════════════════════════════════════
	r := gin.Default()

	// Public routes (no auth)
	r.POST("/login", api.LoginHandler)

	// Protected routes - all use channels, no repo dependencies
	apiGroup := r.Group("/api/v1")
	apiGroup.Use(api.JWTMiddleware())
	{
		api.RegisterEntityRoutes[models.CredentialProfile](apiGroup, "/credentials", "CredentialProfile", crudRequestChan)
		api.RegisterEntityRoutes[models.Monitor](apiGroup, "/monitors", "Monitor", crudRequestChan)
		api.RegisterEntityRoutes[models.DiscoveryProfile](apiGroup, "/discovery_profiles", "DiscoveryProfile", crudRequestChan)
		api.RegisterEntityRoutes[models.Device](apiGroup, "/devices", "Device", crudRequestChan)
		api.RegisterMetricsRoute(apiGroup, metricRequestChan)

		// Manual provisioning endpoints (zero repository dependencies)
		apiGroup.POST("/discovery_profiles/:id/run", api.RunDiscoveryHandler(provisioningChan))
		apiGroup.POST("/devices/:id/provision", api.ProvisionDeviceHandler(provisioningChan))
	}

	// ══════════════════════════════════════════════════════════════
	// START SERVER
	// ══════════════════════════════════════════════════════════════
	if config.TLSCertFile != "" && config.TLSKeyFile != "" {
		slog.Info("Starting HTTPS server", "port", 8443)
		if err := r.RunTLS(":8443", config.TLSCertFile, config.TLSKeyFile); err != nil {
			slog.Error("Server failed to start", "error", err)
			os.Exit(1)
		}
	} else {
		slog.Info("Starting HTTP server", "port", 8080)
		if err := r.Run(":8080"); err != nil {
			slog.Error("Server failed to start", "error", err)
			os.Exit(1)
		}
	}
}
