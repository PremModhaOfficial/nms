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
	"nms/pkg/poller"
	"nms/pkg/scheduler"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// services holds background workers that process events
type services struct {
	sched          *scheduler.Scheduler
	poll           *poller.Poller
	discService    *discovery.DiscoveryService
	metricsService *persistence.MetricsService
	entityService  *persistence.EntityService
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

	loadInitialData(db, services.sched)

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
	sched := scheduler.NewScheduler(
		deviceChan,
		credentialChan,
		schedulerToPollerChan,
		fpingPath,
		conf.PollIntervalSec,
		conf.AvCheckTimeoutMs,
		conf.AvCheckRetries,
	)

	poll := poller.NewPoller(
		conf.PluginsDir,
		conf.EncryptionKey,
		conf.PollWorkerCount,
		DataBufferSize,
		schedulerToPollerChan,
		pollResultChan,
	)

	discService := discovery.NewDiscoveryService(
		discProfileChan,
		discResultChan,
		conf.PluginsDir,
		conf.EncryptionKey,
		conf.DiscWorkerCount,
		EventBufferSize,
	)

	metricsService := persistence.NewMetricsService(
		pollResultChan,
		metricRequestChan,
		db,
		conf.MetricsDefaultLimit,
		conf.MetricsDefaultLookbackHours,
	)

	entityService := persistence.NewEntityService(
		discResultChan,
		provisioningEventChan,
		crudRequestChan,
		db,
		discProfileChan,
		deviceChan,
		credentialChan,
	)

	svc := &services{
		sched:          sched,
		poll:           poll,
		discService:    discService,
		metricsService: metricsService,
		entityService:  entityService,
	}

	channels := &apiChannels{
		crudRequest:       crudRequestChan,
		metricRequest:     metricRequestChan,
		provisioningEvent: provisioningEventChan,
	}

	return svc, channels
}

func loadInitialData(db *gorm.DB, sched *scheduler.Scheduler) {
	var initialDevices []*models.Device
	if err := db.Find(&initialDevices).Error; err != nil {
		slog.Error("Failed to list devices for scheduler", "error", err)
	}
	var initialCreds []*models.CredentialProfile
	if err := db.Find(&initialCreds).Error; err != nil {
		slog.Error("Failed to list credentials for scheduler", "error", err)
	}
	sched.LoadCache(initialDevices, initialCreds)
	slog.Info("Scheduler cache loaded from database")
}

func startServices(ctx context.Context, svc *services) {
	go svc.sched.Run(ctx)
	go svc.poll.Run(ctx)
	go svc.discService.Start(ctx)
	go svc.metricsService.Run(ctx)
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
