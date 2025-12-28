package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"nms/pkg/config"

	_ "github.com/jackc/pgx/v5/stdlib" // Register pgx driver for database/sql
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Connect initializes the database connection
func Connect(cfg *config.Config) (*gorm.DB, error) {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=UTC",
		cfg.DBHost, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBPort)

	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	}

	db, err := gorm.Open(postgres.Open(dsn), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.DBMaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DBMaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.DBConnMaxLifeMins) * time.Minute)

	slog.Info("Configured connection pool",
		"component", "Database",
		"max_open", cfg.DBMaxOpenConns,
		"max_idle", cfg.DBMaxIdleConns,
		"max_life_mins", cfg.DBConnMaxLifeMins,
	)

	slog.Info("Connected to database", "component", "Database")
	return db, nil
}

// ConnectRaw creates a raw sql.DB connection pool without GORM overhead.
// Used for high-performance operations like metrics that don't need ORM features.
func ConnectRaw(cfg *config.Config, poolName string, maxOpen, maxIdle int) (*sql.DB, error) {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=UTC",
		cfg.DBHost, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBPort)

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open raw connection: %w", err)
	}

	// Verify connection
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Configure pool
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.DBConnMaxLifeMins) * time.Minute)

	slog.Info("Connected raw pool",
		"component", "Database",
		"pool", poolName,
		"max_open", maxOpen,
		"max_idle", maxIdle,
	)

	return sqlDB, nil
}
