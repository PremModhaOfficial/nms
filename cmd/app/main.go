package main

import (
	"context"
	"log/slog"
	"os"

	"nms/pkg/config"

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
	conf, err := config.LoadConfig(".")
	if err != nil {
		slog.Error("Failed to load conf", "error", err)
		os.Exit(1)
	}
	slog.Info("Config loaded", "fping_path", conf.FpingPath, "tick_interval", conf.SchedulerTickIntervalSeconds)

	// Initialize Auth Service
	auth := api.Auth(conf)

	// ══════════════════════════════════════════════════════════════
	// DATABASE
	// ══════════════════════════════════════════════════════════════
	db, err := database.Connect(conf)
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
	monitorChan := make(chan models.Event, conf.InternalQueueSize)
	credentialChan := make(chan models.Event, conf.InternalQueueSize)
	discProfileChan := make(chan models.Event, conf.InternalQueueSize)
	pollResultChan := make(chan []plugin.Result, conf.InternalQueueSize)
	discResultChan := make(chan plugin.Result, conf.InternalQueueSize)
	schedulerToPollerChan := make(chan []*models.Monitor, conf.PollerBatchSize)
	provisioningEventChan := make(chan models.Event, conf.InternalQueueSize)

	// New request-reply channels for CRUD and metrics
	crudRequestChan := make(chan models.Request, conf.InternalQueueSize)
	metricRequestChan := make(chan models.Request, conf.InternalQueueSize)

	// Services will create their own repositories from db

	// ══════════════════════════════════════════════════════════════
	// SERVICES
	// ══════════════════════════════════════════════════════════════
	sched := scheduler.NewScheduler(
		monitorChan,
		credentialChan,
		schedulerToPollerChan,
		// configs
		conf.FpingPath,
		conf.SchedulerTickIntervalSeconds,
		conf.FpingTimeoutMs,
		conf.FpingRetryCount,
	)

	poll := poller.NewPoller(
		conf.PluginsDir,
		conf.EncryptionKey,
		conf.PollingWorkerConcurrency,
		conf.InternalQueueSize,
		schedulerToPollerChan,
		pollResultChan,
	)

	discService := discovery.NewDiscoveryService(
		discProfileChan,
		discResultChan,
		conf.PluginsDir,
		conf.EncryptionKey,
		conf.DiscoveryWorkerConcurrency,
		conf.InternalQueueSize,
	)

	// MetricsService handles high-volume poll results and metric queries
	metricsService := persistence.NewMetricsService(
		pollResultChan,
		metricRequestChan,
		db,
		conf.MetricsDefaultLimit,
		conf.MetricsDefaultLookbackHours,
	)

	// EntityService handles CRUD, discovery provisioning, and commands
	entityService := persistence.NewEntityService(
		discResultChan,
		provisioningEventChan,
		crudRequestChan,
		db,
		discProfileChan,
		monitorChan,
		credentialChan,
	)

	// ══════════════════════════════════════════════════════════════
	// INITIAL CACHE LOAD
	// ══════════════════════════════════════════════════════════════
	contxt := context.Background()
	var initialMonitors []*models.Monitor
	if err := db.Find(&initialMonitors).Error; err != nil {
		slog.Error("Failed to list monitors for scheduler", "error", err)
	}
	var initialCreds []*models.CredentialProfile
	if err := db.Find(&initialCreds).Error; err != nil {
		slog.Error("Failed to list credentials for scheduler", "error", err)
	}
	sched.LoadCache(initialMonitors, initialCreds)
	slog.Info("Scheduler cache loaded from database")

	// ══════════════════════════════════════════════════════════════
	// START SERVICES
	// ══════════════════════════════════════════════════════════════
	go sched.Run(contxt)
	go poll.Run(contxt)
	go discService.Start(contxt)
	go metricsService.Run(contxt)
	go entityService.Run(contxt)

	// ══════════════════════════════════════════════════════════════
	// ROUTER SETUP
	// ══════════════════════════════════════════════════════════════
	router := gin.Default()

	// Public routes (no auth)
	router.POST("/login", auth.LoginHandler)

	// Protected routes - all use channels, no repo dependencies
	apiGroup := router.Group("/api/v1")
	apiGroup.Use(auth.JWTMiddleware())
	{
		api.RegisterEntityRoutes[models.CredentialProfile](apiGroup, "/credentials", "CredentialProfile", conf.EncryptionKey, crudRequestChan)
		api.RegisterEntityRoutes[models.Monitor](apiGroup, "/monitors", "Monitor", conf.EncryptionKey, crudRequestChan)
		api.RegisterEntityRoutes[models.DiscoveryProfile](apiGroup, "/discovery_profiles", "DiscoveryProfile", conf.EncryptionKey, crudRequestChan)
		api.RegisterEntityRoutes[models.Device](apiGroup, "/devices", "Device", conf.EncryptionKey, crudRequestChan)
		api.RegisterMetricsRoute(apiGroup, metricRequestChan)

		// Manual provisioning endpoints (zero repository dependencies)
		apiGroup.POST("/discovery_profiles/:id/run", api.RunDiscoveryHandler(provisioningEventChan))
		apiGroup.POST("/devices/:id/provision", api.ProvisionDeviceHandler(provisioningEventChan))
	}

	// ══════════════════════════════════════════════════════════════
	// START SERVER
	// ══════════════════════════════════════════════════════════════
	if conf.TLSCertFile != "" && conf.TLSKeyFile != "" {
		slog.Info("Starting HTTPS app", "port", 8443)
		if err := router.RunTLS(":8443", conf.TLSCertFile, conf.TLSKeyFile); err != nil {
			slog.Error("Server failed to start", "error", err)
			os.Exit(1)
		}
	} else {
		slog.Info("Starting HTTP app", "port", 8080)
		if err := router.Run(":8080"); err != nil {
			slog.Error("Server failed to start", "error", err)
			os.Exit(1)
		}
	}
}
