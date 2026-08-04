package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"nms/pkg/Services/discovery"
	"nms/pkg/Services/monitorFailure"
	"nms/pkg/Services/persistence"
	"nms/pkg/Services/polling"
	"nms/pkg/Services/scheduling"

	"nms/pkg/config"
	"nms/pkg/tracex"

	"nms/pkg/api"
	"nms/pkg/database"
	"nms/pkg/models"
	"nms/pkg/plugin"

	"github.com/jmoiron/sqlx"
)

// services holds background workers that process events
type services struct {
	sched          *scheduling.Scheduler
	poll           *polling.Poller
	discService    *discovery.DiscoveryService
	metricsService *persistence.MetricsService
	entityService  *persistence.EntityService
	failureService *monitorFailure.FailureService
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

	// Initialize the trace store and its span exporter. The returned shutdown
	// hook flushes pending spans at exit.
	shutdown := tracex.Init()
	defer shutdown()

	// Fail fast on insecure secrets in production; warn otherwise.
	if err := conf.ValidateSecrets(); err != nil {
		if os.Getenv("APP_ENV") == "production" {
			slog.Error("Refusing to start with insecure secrets", "error", err)
			os.Exit(1)
		}
		slog.Warn("Security validation warning", "error", err)
	}

	// In production, never serve the management API over plain HTTP (it
	// carries JWTs, encryption keys, and admin hashes). Fail fast unless TLS
	// is configured.
	if os.Getenv("APP_ENV") == "production" && (conf.TLSCertFile == "" || conf.TLSKeyFile == "") {
		slog.Error("Refusing to start in production without TLS (set TLS_CERT_FILE and TLS_KEY_FILE)")
		os.Exit(1)
	}

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

	router := initRouter(conf, auth, channels)

	// Configure HTTP server. TLS when cert+key are set, plain HTTP otherwise.
	addr := ":8080"
	if conf.TLSCertFile != "" && conf.TLSKeyFile != "" {
		addr = ":8443"
	}
	server := newHTTPServer(addr, router)
	slog.Info("Starting app", "addr", addr)
	go func() {
		var err error
		if conf.TLSCertFile != "" && conf.TLSKeyFile != "" {
			err = server.ListenAndServeTLS(conf.TLSCertFile, conf.TLSKeyFile)
		} else {
			err = server.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			slog.Error("Server failed", "error", err)
		}
	}()

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
	conf, err := config.LoadConfig()
	if err != nil {
		slog.Error("Failed to load conf", "error", err)
		os.Exit(1)
	}
	slog.Info("Config loaded", "poll_interval", conf.PollIntervalSec)
	return conf
}

func initDatabase(conf *config.Config) *sqlx.DB {
	db, err := database.Connect(conf)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	return db
}

func initServices(conf *config.Config, db *sqlx.DB, fpingPath string) (*services, *apiChannels) {
	// ══════════════════════════════════════════════════════════════
	// COMMUNICATION CHANNELS - One per topic
	// ══════════════════════════════════════════════════════════════
	deviceChan := make(chan models.Event, EventBufferSize)
	discProfileChan := make(chan models.Event, EventBufferSize)
	discResultChan := make(chan plugin.Result, EventBufferSize)
	pollResultChan := make(chan []plugin.Result, DataBufferSize)
	schedulerToPollerChan := make(chan []*models.Device, ControlBufferSize)
	failureChan := make(chan models.Event, EventBufferSize) // Shared by Scheduler + MetricsWriter

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
	)

	// Scheduler uses crudRequestChan to request devices from EntityService
	sched := scheduling.NewScheduler(
		deviceChan,
		crudRequestChan,
		schedulerToPollerChan,
		failureChan,
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

	// Create separate DB pools for metrics components, sharing the main pool settings.
	metricsWriteDB, err := database.ConnectRaw(
		conf, "MetricsWrite",
		conf.DBMaxOpenConns, conf.DBMaxIdleConns,
	)
	if err != nil {
		slog.Error("Failed to create MetricsWrite DB pool", "error", err)
		os.Exit(1)
	}

	metricsReadDB, err := database.ConnectRaw(
		conf, "MetricsRead",
		conf.DBMaxOpenConns, conf.DBMaxIdleConns,
	)
	if err != nil {
		slog.Error("Failed to create MetricsRead DB pool", "error", err)
		os.Exit(1)
	}

	metricsService := persistence.NewMetricsService(
		pollResultChan,
		metricRequestChan,
		metricsWriteDB,
		metricsReadDB,
		conf.MetricsWorkerCount,
		failureChan,
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

	// FailureService tracks failures and deactivates devices
	healthMonitor := monitorFailure.NewHealthMonitor(
		failureChan,
		crudRequestChan,
		conf.FailureWindowMin,
		conf.FailureThreshold,
	)

	svc := &services{
		sched:          sched,
		poll:           poll,
		discService:    discService,
		metricsService: metricsService,
		entityService:  entityService,
		failureService: healthMonitor,
	}

	channels := &apiChannels{
		crudRequest:       crudRequestChan,
		metricRequest:     metricRequestChan,
		provisioningEvent: provisioningEventChan,
	}

	return svc, channels
}

// newHTTPServer builds an http.Server with explicit timeouts so slowloris and
// slow-body clients cannot hold connections open indefinitely.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
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
	go svc.metricsService.Run(ctx)
	go svc.entityService.Run(ctx)
	go svc.failureService.Run(ctx)
}

func initRouter(conf *config.Config, auth *api.JwtAuth, channels *apiChannels) http.Handler {
	apiMux := http.NewServeMux()

	// Public routes (no auth)
	apiMux.HandleFunc("POST /login", auth.LoginHandler)

	// Protected routes. Every pattern is wrapped in the JWT middleware.
	protect := func(pattern string, h http.Handler) {
		apiMux.Handle(pattern, auth.JWTMiddleware()(h))
	}
	api.RegisterEntityRoutes[models.CredentialProfile](protect, "/api/v1/credentials", "CredentialProfile", conf.EncryptionKey, channels.crudRequest)
	api.RegisterEntityRoutes[models.Device](protect, "/api/v1/devices", "Device", conf.EncryptionKey, channels.crudRequest)
	api.RegisterEntityRoutes[models.DiscoveryProfile](protect, "/api/v1/discovery_profiles", "DiscoveryProfile", conf.EncryptionKey, channels.crudRequest)
	api.RegisterMetricsRoute(protect, "/api/v1/metrics", channels.metricRequest)
	protect("POST /api/v1/discovery_profiles/{id}/run", api.RunDiscoveryHandler(channels.provisioningEvent, channels.crudRequest))
	protect("POST /api/v1/devices/{id}/provision", api.ProvisionDeviceHandler(channels.provisioningEvent))

	// Dev dashboard API (traces + topology), same JWT protection as the rest.
	protect("GET /api/v1/topology", http.HandlerFunc(api.TopologyHandler))
	protect("GET /api/v1/traces", http.HandlerFunc(api.TracesListHandler))
	protect("GET /api/v1/traces/{id}", http.HandlerFunc(api.TraceGetHandler))

	// Security headers and body cap apply to the API surface only. The static
	// dashboard gets its own looser CSP below, so the API keeps the strict
	// default-src 'none' policy.
	var apiHandler http.Handler = apiMux
	apiHandler = api.SecurityHeaders()(apiHandler)
	apiHandler = api.MaxBodyBytes(1 << 20)(apiHandler) // 1 MiB request body cap

	// Longest-pattern wins on the routing mux, so /api/ and /login keep
	// precedence over "/" (the static dashboard).
	root := http.NewServeMux()
	root.Handle("/api/", apiHandler)
	root.Handle("/login", apiHandler)
	root.Handle("/", dashboardHandler())

	// Trace capture wraps everything, outermost, so auth failures and 5xx
	// responses are traced too.
	return api.TraceMiddleware()(root)
}

// dashboardHandler serves the embedded dev dashboard. Extensionless paths
// fall back to index.html (SPA), everything else goes to the file server. The
// CSP is loosened only for this handler so the dashboard can run script, but
// still same-origin only.
func dashboardHandler() http.Handler {
	fileServer := http.FileServer(WebFiles())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if filepath.Ext(r.URL.Path) == "" {
			// SPA fallback: serve index.html for extensionless client routes.
			clone := r.Clone(r.Context())
			clone.URL.Path = "/"
			fileServer.ServeHTTP(w, clone)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
