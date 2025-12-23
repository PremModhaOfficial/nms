package main

import (
	"context"
	"log/slog"
	"os"

	"nms/pkg/api"
	"nms/pkg/config"
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
	cfg, err := config.LoadConfig(".")
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}
	slog.Info("Config loaded", "fping_path", cfg.FpingPath, "tick_interval", cfg.SchedulerTickIntervalSeconds)

	// Initialize Auth Service
	authSvc := api.NewAuthService(cfg)

	// ══════════════════════════════════════════════════════════════
	// DATABASE
	// ══════════════════════════════════════════════════════════════
	db, err := database.Connect(cfg)
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
	provisioningCommandChan := make(chan models.Event, 100)

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
		cfg.FpingPath,
		cfg.SchedulerTickIntervalSeconds,
		cfg.FpingTimeoutMs,
		cfg.FpingRetryCount,
	)

	poll := poller.NewPoller(
		cfg.PluginsDir,
		cfg.EncryptionKey,
		cfg.PollingWorkerConcurrency,
		schedulerToPollerChan,
		pollResultChan,
	)

	discService := discovery.NewDiscoveryService(
		discProfileChan,
		discResultChan,
		cfg.PluginsDir,
		cfg.EncryptionKey,
		cfg.DiscoveryWorkerConcurrency,
	)

	// MetricsService handles high-volume poll results and metric queries
	metricsService := persistence.NewMetricsService(
		pollResultChan,
		metricRequestChan,
		metricRepo,
		db,
	)

	// EntityService handles CRUD, discovery provisioning, and commands
	entityService := persistence.NewEntityService(
		discResultChan,
		provisioningCommandChan,
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
	go metricsService.Run(ctx)
	go entityService.Run(ctx)

	// ══════════════════════════════════════════════════════════════
	// ROUTER SETUP
	// ══════════════════════════════════════════════════════════════
	r := gin.Default()

	// Public routes (no auth)
	r.POST("/login", authSvc.LoginHandler)

	// Protected routes - all use channels, no repo dependencies
	apiGroup := r.Group("/api/v1")
	apiGroup.Use(authSvc.JWTMiddleware())
	{
		api.RegisterEntityRoutes[models.CredentialProfile](apiGroup, "/credentials", "CredentialProfile", cfg.EncryptionKey, crudRequestChan)
		api.RegisterEntityRoutes[models.Monitor](apiGroup, "/monitors", "Monitor", cfg.EncryptionKey, crudRequestChan)
		api.RegisterEntityRoutes[models.DiscoveryProfile](apiGroup, "/discovery_profiles", "DiscoveryProfile", cfg.EncryptionKey, crudRequestChan)
		api.RegisterEntityRoutes[models.Device](apiGroup, "/devices", "Device", cfg.EncryptionKey, crudRequestChan)
		api.RegisterMetricsRoute(apiGroup, metricRequestChan)

		// Manual provisioning endpoints (zero repository dependencies)
		apiGroup.POST("/discovery_profiles/:id/run", api.RunDiscoveryHandler(provisioningCommandChan))
		apiGroup.POST("/devices/:id/provision", api.ProvisionDeviceHandler(provisioningCommandChan))
	}

	// ══════════════════════════════════════════════════════════════
	// START SERVER
	// ══════════════════════════════════════════════════════════════
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		slog.Info("Starting HTTPS server", "port", 8443)
		if err := r.RunTLS(":8443", cfg.TLSCertFile, cfg.TLSKeyFile); err != nil {
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
