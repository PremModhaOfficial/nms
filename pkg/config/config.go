package config

import (
	"strings"

	"github.com/spf13/viper"
)

// Config stores all configuration of the application.
// The values are read by viper from a config file or environment variable.
type Config struct {
	// Database Configurations
	DBHost     string `mapstructure:"DB_HOST"`
	DBUser     string `mapstructure:"DB_USER"`
	DBPassword string `mapstructure:"DB_PASSWORD"`
	DBName     string `mapstructure:"DB_NAME"`
	DBPort     string `mapstructure:"DB_PORT"`

	// Server Configurations
	ServerAddress string `mapstructure:"SERVER_ADDRESS"`
	TLSCertFile   string `mapstructure:"TLS_CERT_FILE"`
	TLSKeyFile    string `mapstructure:"TLS_KEY_FILE"`

	// General Configurations
	PluginsDir string `mapstructure:"PLUGINS_DIR"`

	// Worker Configurations
	FpingPath                  string `mapstructure:"FPING_PATH"`
	PollingWorkerConcurrency   int    `mapstructure:"POLLING_WORKER_CONCURRENCY"`
	DiscoveryWorkerConcurrency int    `mapstructure:"DISCOVERY_WORKER_CONCURRENCY"`

	// Scheduler Configurations
	SchedulerTickIntervalSeconds int `mapstructure:"SCHEDULER_TICK_INTERVAL_SECONDS"`
	FpingTimeoutMs               int `mapstructure:"FPING_TIMEOUT_MS"`
	FpingRetryCount              int `mapstructure:"FPING_RETRY_COUNT"`

	// Security/Encryption Configurations
	JWTSecret     string `mapstructure:"JWT_SECRET"`
	EncryptionKey string `mapstructure:"NMS_SECRET"`
	AdminUser     string `mapstructure:"NMS_ADMIN_USER"`
	AdminHash     string `mapstructure:"NMS_ADMIN_HASH"`
}

// LoadConfig reads configuration from file or environment variables.
func LoadConfig(path string) (*Config, error) {
	v := viper.New()

	v.AddConfigPath(path)
	v.SetConfigName("app")
	v.SetConfigType("yaml")

	// Allow Viper to read Environment Variables
	v.AutomaticEnv()

	// Map dots to underscores in environment variables
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Set Defaults
	v.SetDefault("DB_HOST", "localhost")
	v.SetDefault("DB_USER", "nmslite")
	v.SetDefault("DB_PASSWORD", "nmslite")
	v.SetDefault("DB_NAME", "nmslite")
	v.SetDefault("DB_PORT", "5432")
	v.SetDefault("PLUGINS_DIR", "plugins")
	v.SetDefault("FPING_PATH", "/usr/bin/fping")
	v.SetDefault("POLLING_WORKER_CONCURRENCY", 5)
	v.SetDefault("DISCOVERY_WORKER_CONCURRENCY", 3)
	v.SetDefault("SCHEDULER_TICK_INTERVAL_SECONDS", 5)
	v.SetDefault("FPING_TIMEOUT_MS", 500)
	v.SetDefault("FPING_RETRY_COUNT", 2)
	v.SetDefault("JWT_SECRET", "default-insecure-secret-change-me")
	v.SetDefault("NMS_SECRET", "1234567890123456789012345678901212345678901234567890123456789012")
	v.SetDefault("NMS_ADMIN_USER", "admin")
	v.SetDefault("NMS_ADMIN_HASH", "$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu") // bcrypt hash of "admin"

	if err := v.ReadInConfig(); err != nil {
		// It's okay if config file is not found, we use defaults and env vars
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, err
	}

	return &config, nil
}
