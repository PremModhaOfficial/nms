package main

import (
	"context"
	"log/slog"
	"os"

	"nms/pkg/api"
	"nms/pkg/communication"
	"nms/pkg/database"
	"nms/pkg/datawriter"
	"nms/pkg/discovery"
	"nms/pkg/models"
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
	deviceChan := make(chan models.Event, 100)
	pollResultChan := make(chan []plugin.Result, 100)
	discResultChan := make(chan plugin.Result, 100)
	schedulerToPollerChan := make(chan []*models.Monitor, 10)

	// ══════════════════════════════════════════════════════════════
	// REPOSITORIES - Base and Publishing wrappers
	// ══════════════════════════════════════════════════════════════
	baseCredRepo := database.NewGormRepository[models.CredentialProfile](db)
	baseMonRepo := database.NewGormRepository[models.Monitor](db)
	baseDiscProfileRepo := database.NewPreloadingDiscoveryProfileRepo(db) // Auto-preloads CredentialProfile
	baseDeviceRepo := database.NewGormRepository[models.Device](db)
	baseMetricRepo := database.NewGormRepository[models.Metric](db)
	metricRepo := database.NewMetricRepository(baseMetricRepo)

	// Publishing wrappers for EDA - just channels, no DB dependencies
	credRepo := communication.NewPublishingRepo[models.CredentialProfile](baseCredRepo, credentialChan)
	monRepo := communication.NewPublishingRepo[models.Monitor](baseMonRepo, monitorChan)
	discProfileRepo := communication.NewPublishingRepo[models.DiscoveryProfile](baseDiscProfileRepo, discProfileChan)
	deviceRepo := communication.NewPublishingRepo[models.Device](baseDeviceRepo, deviceChan)

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

	dataWriter := datawriter.NewWriter(
		pollResultChan,
		discResultChan,
		db,
		deviceRepo,
		monRepo,
	)

	// ══════════════════════════════════════════════════════════════
	// INITIAL CACHE LOAD
	// ══════════════════════════════════════════════════════════════
	ctx := context.Background()
	initialMonitors, err := baseMonRepo.List(ctx)
	if err != nil {
		slog.Error("Failed to list monitors for scheduler", "error", err)
	}
	initialCreds, err := baseCredRepo.List(ctx)
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
	go dataWriter.Run(ctx)

	// ══════════════════════════════════════════════════════════════
	// API HANDLERS
	// ══════════════════════════════════════════════════════════════
	credHandler := api.NewCrudHandler[models.CredentialProfile](credRepo)
	monHandler := api.NewCrudHandler[models.Monitor](monRepo)
	discProfileHandler := api.NewCrudHandler[models.DiscoveryProfile](discProfileRepo)
	deviceHandler := api.NewCrudHandler[models.Device](baseDeviceRepo) // Read-only, no publishing
	metricHandler := api.NewMetricHandler(metricRepo)

	// ══════════════════════════════════════════════════════════════
	// ROUTER SETUP
	// ══════════════════════════════════════════════════════════════
	r := gin.Default()

	// Public routes (no auth)
	r.POST("/login", api.LoginHandler)

	// Protected routes
	apiGroup := r.Group("/api/v1")
	apiGroup.Use(api.JWTMiddleware())
	{
		credHandler.RegisterRoutes(apiGroup, "/credentials")
		monHandler.RegisterRoutes(apiGroup, "/monitors")
		discProfileHandler.RegisterRoutes(apiGroup, "/discovery_profiles")
		deviceHandler.RegisterRoutes(apiGroup, "/devices")
		metricHandler.RegisterRoutes(apiGroup)
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
