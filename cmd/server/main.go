package main

import (
	"context"
	"log"

	"nms/pkg/api"
	"nms/pkg/database"
	"nms/pkg/models"
	"nms/pkg/plugin"
	"nms/pkg/poller"
	"nms/pkg/scheduler"

	"github.com/gin-gonic/gin"
)

func run() error {
	return nil
}

func main() {
	// Load Configuration
	config, err := models.LoadConfig(".")
	if err != nil {
		log.Printf("Warning: Failed to load config: %v. Using defaults.", err)
		// Set defaults
		config.FpingPath = "/usr/bin/fping"
		config.SchedulerTickIntervalSeconds = 5
		config.FpingTimeoutMs = 500
		config.FpingRetryCount = 2
	}
	log.Printf("Config loaded: FpingPath=%s, TickInterval=%ds", config.FpingPath, config.SchedulerTickIntervalSeconds)

	// Initialize Database
	db, err := database.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Initialize Repositories (Pure Gorm)
	credRepo := database.NewGormRepository[models.CredentialProfile](db)
	discRepo := database.NewGormRepository[models.DiscoveryProfile](db)
	devRepo := database.NewGormRepository[models.Device](db)
	monRepo := database.NewGormRepository[models.Monitor](db)

	// Channels for Scheduler -> Poller communication
	schedulerOutChan := make(chan []*models.Monitor, 10)
	pollerResultChan := make(chan []plugin.Result, 10)

	// Initialize Poller with worker pool
	poll := poller.NewPoller("plugins", 5, schedulerOutChan, pollerResultChan)

	// Initialize Scheduler with config
	sched := scheduler.NewScheduler(
		schedulerOutChan,
		config.FpingPath,
		config.SchedulerTickIntervalSeconds,
		config.FpingTimeoutMs,
		config.FpingRetryCount,
	)

	// Load Cache (Initial Load)
	ctx := context.Background()
	initialMonitors, err := monRepo.List(ctx)
	if err != nil {
		log.Printf("Failed to list monitors for scheduler: %v", err)
	}
	initialCreds, err := credRepo.List(ctx)
	if err != nil {
		log.Printf("Failed to list credentials for scheduler: %v", err)
	}

	sched.LoadCache(initialMonitors, initialCreds)
	log.Println("Scheduler cache loaded from database")

	// Start Scheduler and Poller
	go sched.Run(ctx)
	go poll.Run(ctx)

	// Start result consumer (logs results for now)
	go func() {
		for results := range pollerResultChan {
			log.Printf("Received %d poll results", len(results))

			// TODO: Implement result persistence.
			// 1. Iterate through results.
			// 2. Map metrics to a database schema (e.g., a 'metrics' table).
			// 3. Batch insert results into the database for efficiency.
			// 4. Update the 'last_polled_at' timestamp on the Monitor record.

			for _, result := range results {
				if result.Success {
					log.Printf("  [%s:%d] Success: %d metrics", result.Target, result.Port, len(result.Metrics))
				} else {
					log.Printf("  [%s:%d] Error: %s", result.Target, result.Port, result.Error)
				}
			}
		}
	}()

	// Initialize Services (Coordinator Layer)

	// TODO: Initialize Discovery Service and Workers
	// 1. Create discoveryPool := worker.NewDiscoveryPool(workerCount)
	// 2. Create discoveryService := worker.NewDiscoveryService(discoveryPool, devRepo, monRepo, credRepo)
	// 3. Start discoveryService: go discoveryService.Start(ctx)

	// Initialize Handlers
	// For Monitors and Creds, use the Service (which acts as a Repo)

	// For others, use Repo directly
	discHandler := api.NewCrudHandler[models.DiscoveryProfile](discRepo)
	// TODO: Wrap discRepo to trigger discoveryService.RunProfile when a profile is created or updated.

	devHandler := api.NewCrudHandler[models.Device](devRepo)
	credHandler := api.NewCrudHandler[models.CredentialProfile](credRepo)
	monHandler := api.NewCrudHandler[models.Monitor](monRepo)

	// Setup Router
	r := gin.Default()

	// Register Routes
	apiGroup := r.Group("/api/v1")
	{
		credHandler.RegisterRoutes(apiGroup, "/credentials")
		discHandler.RegisterRoutes(apiGroup, "/discovery")
		devHandler.RegisterRoutes(apiGroup, "/devices")
		monHandler.RegisterRoutes(apiGroup, "/monitors")
	}

	// Start Server
	log.Println("Starting server on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
