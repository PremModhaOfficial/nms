package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nms/pkg/config"

	"nms/pkg/api"
	"nms/pkg/database"
	"nms/pkg/discovery"
	"nms/pkg/models"
	"nms/pkg/persistence"
	"nms/pkg/plugin"
	"nms/pkg/polling"
	"nms/pkg/scheduling"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// services holds background workers that process events
type services struct {
	sched         *scheduling.Scheduler
	poll          *polling.Poller
	discService   *discovery.DiscoveryService
	metricsWriter *persistence.MetricsWriter
	metricsReader *persistence.MetricsReader
	entityService *persistence.EntityService
}

// apiChannels holds request channels used by API handlers
type apiChannels struct {
	crudRequest       chan models.Request
	metricRequest     chan models.Request
	provisioningEvent chan models.Event
}

// channel buffer sizes based on usecases
const (
	DataBufferSize    = 1000 // high-volume result channels
	EventBufferSize   = 100  // standard event/request channels
	ControlBufferSize = 50   // low-volume control/batch channels
)

func main() {
	initLogger()
	conf := loadConfig()
	auth := api.Auth(conf)
	db := initDatabase(conf)

	// Automatically find fping path
	fpingPath, err := config.FindFpingPath()
	if err != nil {
		slog.Error("Fping discovery failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Fping discovered", "path", fpingPath)

	services, channels := initServices(conf, db, fpingPath)

	// Load caches in EntityService and initialize Scheduler queue
	loadInitialData(services.entityService, services.sched)

	// Create context that cancels on SIGINT or SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startServices(ctx, services)

	if err := conf.ValidateSecrets(); err != nil {
		slog.Warn("Security validation warning", "error", err)
	}

	router := initRouter(conf, auth, channels)

	// Configure HTTP server
	var addr string
	var server *http.Server

	if conf.TLSCertFile != "" && conf.TLSKeyFile != "" {
		addr = ":8443"
		server = &http.Server{Addr: addr, Handler: router}
		slog.Info("Starting HTTPS app", "port", 8443)
		go func() {
			if err := server.ListenAndServeTLS(conf.TLSCertFile, conf.TLSKeyFile); err != nil && err != http.ErrServerClosed {
				slog.Error("Server failed", "error", err)
			}
		}()
	} else {
		addr = ":8080"
		server = &http.Server{Addr: addr, Handler: router}
		slog.Info("Starting HTTP app", "port", 8080)
		go func() {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("Server failed", "error", err)
			}
		}()
	}

	// Wait for shutdown signal
	<-ctx.Done()
	slog.Info("Shutdown signal received, stopping services...")

	// Give services time to finish
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	slog.Info("Graceful shutdown complete")
}

func initLogger() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
}

func loadConfig() *config.Config {
	conf, err := config.LoadConfig(".")
	if err != nil {
		slog.Error("Failed to load conf", "error", err)
		os.Exit(1)
	}
	slog.Info("Config loaded", "poll_interval", conf.PollIntervalSec)
	return conf
}

func initDatabase(conf *config.Config) *gorm.DB {
	db, err := database.Connect(conf)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	return db
}

func initServices(conf *config.Config, db *gorm.DB, fpingPath string) (*services, *apiChannels) {
	// ══════════════════════════════════════════════════════════════
	// COMMUNICATION CHANNELS - One per topic
	// ══════════════════════════════════════════════════════════════
	deviceChan := make(chan models.Event, EventBufferSize)
	credentialChan := make(chan models.Event, EventBufferSize)
	discProfileChan := make(chan models.Event, EventBufferSize)
	discResultChan := make(chan plugin.Result, EventBufferSize)
	pollResultChan := make(chan []plugin.Result, DataBufferSize)
	schedulerToPollerChan := make(chan []*models.Device, ControlBufferSize)

	crudRequestChan := make(chan models.Request, EventBufferSize)
	metricRequestChan := make(chan models.Request, EventBufferSize)
	provisioningEventChan := make(chan models.Event, EventBufferSize)

	// ══════════════════════════════════════════════════════════════
	// SERVICES
	// ══════════════════════════════════════════════════════════════

	// EntityService needs to be created first as Scheduler and Poller depend on crudRequestChan
	entityService := persistence.NewEntityService(
		discResultChan,
		provisioningEventChan,
		crudRequestChan,
		db,
		discProfileChan,
		deviceChan,
		credentialChan,
	)

	// Scheduler uses crudRequestChan to request devices from EntityService
	sched := scheduling.NewScheduler(
		deviceChan,
		crudRequestChan,
		schedulerToPollerChan,
		fpingPath,
		conf.PollIntervalSec,
		conf.AvCheckTimeoutMs,
		conf.AvCheckRetries,
	)

	// Poller uses crudRequestChan to request credentials from EntityService
	poll := polling.NewPoller(
		conf.PluginsDir,
		conf.EncryptionKey,
		conf.PollWorkerCount,
		DataBufferSize,
		crudRequestChan,
		schedulerToPollerChan,
		pollResultChan,
	)

	// Create separate DB pools for metrics components
	metricsWriterDB, err := database.ConnectRaw(
		conf, "MetricsWriter",
		conf.MetricsWriterMaxOpen, conf.MetricsWriterMaxIdle,
	)
	if err != nil {
		slog.Error("Failed to create MetricsWriter DB pool", "error", err)
		os.Exit(1)
	}

	metricsReaderDB, err := database.ConnectRaw(
		conf, "MetricsReader",
		conf.MetricsReaderMaxOpen, conf.MetricsReaderMaxIdle,
	)
	if err != nil {
		slog.Error("Failed to create MetricsReader DB pool", "error", err)
		os.Exit(1)
	}

	metricsWriter := persistence.NewMetricsWriter(pollResultChan, metricsWriterDB)
	metricsReader := persistence.NewMetricsReader(
		metricRequestChan,
		metricsReaderDB,
		conf.MetricsDefaultLimit,
		conf.MetricsDefaultLookbackHours,
	)

	discService := discovery.NewDiscoveryService(
		discProfileChan,
		discResultChan,
		conf.PluginsDir,
		conf.EncryptionKey,
		conf.DiscWorkerCount,
		EventBufferSize,
	)

	svc := &services{
		sched:         sched,
		poll:          poll,
		discService:   discService,
		metricsWriter: metricsWriter,
		metricsReader: metricsReader,
		entityService: entityService,
	}

	channels := &apiChannels{
		crudRequest:       crudRequestChan,
		metricRequest:     metricRequestChan,
		provisioningEvent: provisioningEventChan,
	}

	return svc, channels
}

func loadInitialData(entityService *persistence.EntityService, sched *scheduling.Scheduler) {
	// Load caches in EntityService
	if err := entityService.LoadCaches(context.Background()); err != nil {
		slog.Error("Failed to load EntityService caches", "error", err)
		os.Exit(1)
	}

	// Initialize Scheduler queue with active device IDs from EntityService
	deviceIDs := entityService.GetActiveDeviceIDs()
	sched.InitQueue(deviceIDs)
	slog.Info("Scheduler queue initialized", "device_count", len(deviceIDs))
}

func startServices(ctx context.Context, svc *services) {
	go svc.sched.Run(ctx)
	go svc.poll.Run(ctx)
	go svc.discService.Start(ctx)
	go svc.metricsWriter.Run(ctx)
	go svc.metricsReader.Run(ctx)
	go svc.entityService.Run(ctx)
}

func initRouter(conf *config.Config, auth *api.JwtAuth, channels *apiChannels) *gin.Engine {
	router := gin.Default()
	router.Use(api.SecurityHeaders())

	// Public routes (no auth)
	router.POST("/login", auth.LoginHandler)

	// Protected routes
	apiGroup := router.Group("/api/v1")
	apiGroup.Use(auth.JWTMiddleware())
	{
		api.RegisterEntityRoutes[models.CredentialProfile](apiGroup, "/credentials", "CredentialProfile", conf.EncryptionKey, channels.crudRequest)
		api.RegisterEntityRoutes[models.Device](apiGroup, "/devices", "Device", conf.EncryptionKey, channels.crudRequest)
		api.RegisterEntityRoutes[models.DiscoveryProfile](apiGroup, "/discovery_profiles", "DiscoveryProfile", conf.EncryptionKey, channels.crudRequest)
		api.RegisterMetricsRoute(apiGroup, channels.metricRequest)

		apiGroup.POST("/discovery_profiles/:id/run", api.RunDiscoveryHandler(channels.provisioningEvent))
		apiGroup.POST("/devices/:id/activate", api.ActivateDeviceHandler(channels.provisioningEvent))
	}

	return router
}
