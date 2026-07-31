package config

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Config stores all configuration of the application. Values come from
// environment variables with the defaults below (matching the old app.yaml).
type Config struct {
	// Database
	DBHost     string
	DBUser     string
	DBPassword string
	DBName     string
	DBPort     string

	// Server
	TLSCertFile    string
	TLSKeyFile     string
	TrustedProxies string

	// General
	PluginsDir string

	// Workers
	PollWorkerCount int
	DiscWorkerCount int

	// Scheduler
	PollIntervalSec  int
	AvCheckTimeoutMs int
	AvCheckRetries   int

	// Security
	JWTSecret     string
	EncryptionKey string
	AdminUser     string
	AdminHash     string

	// Authentication
	SessionDurationHours int

	// Metrics Query Defaults
	MetricsDefaultLimit         int
	MetricsDefaultLookbackHours int

	// Connection Pool Settings (shared by main, write, and read pools)
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifeMins int

	// Health Monitor
	FailureWindowMin int
	FailureThreshold int

	// Metrics Service Worker Pool
	MetricsWorkerCount int
}

// env reads a string env var or returns the default when unset or empty.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envInt reads an int env var or returns the default when unset or unparsable.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		DBHost:                      env("DB_HOST", "localhost"),
		DBUser:                      env("DB_USER", "nmslite"),
		DBPassword:                  env("DB_PASSWORD", "nmslite"),
		DBName:                      env("DB_NAME", "nmslite"),
		DBPort:                      env("DB_PORT", "5432"),
		TLSCertFile:                 env("TLS_CERT_FILE", ""),
		TLSKeyFile:                  env("TLS_KEY_FILE", ""),
		TrustedProxies:              env("TRUSTED_PROXIES", ""),
		PluginsDir:                  env("PLUGINS_DIR", "plugins"),
		PollWorkerCount:             envInt("POLL_WORKER_COUNT", 5),
		DiscWorkerCount:             envInt("DISC_WORKER_COUNT", 3),
		PollIntervalSec:             envInt("POLL_INTERVAL_SEC", 30),
		AvCheckTimeoutMs:            envInt("AV_CHECK_TIMEOUT_MS", 500),
		AvCheckRetries:              envInt("AV_CHECK_RETRIES", 2),
		JWTSecret:                   env("JWT_SECRET", "default-insecure-secret-change-me"),
		EncryptionKey:               env("ENCRYPTION_KEY", "1234567890123456789012345678901212345678901234567890123456789012"),
		AdminUser:                   env("NMS_ADMIN_USER", "admin"),
		AdminHash:                   env("NMS_ADMIN_HASH", "$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu"),
		SessionDurationHours:        envInt("SESSION_DURATION_HOURS", 168),
		MetricsDefaultLimit:         envInt("METRICS_DEFAULT_LIMIT", 100),
		MetricsDefaultLookbackHours: envInt("METRICS_DEFAULT_LOOKBACK_HOURS", 1),
		DBMaxOpenConns:              envInt("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:              envInt("DB_MAX_IDLE_CONNS", 10),
		DBConnMaxLifeMins:           envInt("DB_CONN_MAX_LIFE_MINS", 30),
		FailureWindowMin:            envInt("FAILURE_WINDOW_MIN", 3),
		FailureThreshold:            envInt("FAILURE_THRESHOLD", 3),
		MetricsWorkerCount:          envInt("METRICS_WORKER_COUNT", 4),
	}

	// Validate scheduler tick interval
	if cfg.PollIntervalSec < 20 {
		return nil, errors.New("POLL_INTERVAL_SEC must be at least 20 seconds")
	}

	return cfg, nil
}

// ValidateSecrets ensures critical secrets are not using insecure defaults.
// Call this in production to fail fast if secrets are not properly configured.
func (c *Config) ValidateSecrets() error {
	if c.JWTSecret == "default-insecure-secret-change-me" {
		return errors.New("JWT_SECRET must be changed from default for production")
	}
	if c.EncryptionKey == "1234567890123456789012345678901212345678901234567890123456789012" {
		return errors.New("ENCRYPTION_KEY must be changed from default for production")
	}
	// The default admin hash is the well-known bcrypt hash of "admin"; the
	// default DB password is the widely-known dev value. Both must be changed
	// for a production deployment.
	if c.AdminHash == "$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu" {
		return errors.New("NMS_ADMIN_HASH must be changed from the default 'admin' password for production")
	}
	if c.DBPassword == "nmslite" {
		return errors.New("DB_PASSWORD must be changed from the default for production")
	}
	if strings.TrimSpace(c.DBPassword) == "" {
		return errors.New("DB_PASSWORD must not be empty for production")
	}
	return nil
}

// FindFpingPath attempts to find the fping binary in the system PATH.
func FindFpingPath() (string, error) {
	path, err := exec.LookPath("fping")
	if err != nil {
		return "", errors.New("fping utility not found. Please install it (e.g., 'sudo apt install fping' or 'brew install fping')")
	}
	return path, nil
}
