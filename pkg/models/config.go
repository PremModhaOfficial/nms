package models

import (
	"strings"

	"github.com/spf13/viper"
)

// Config stores all configuration of the application.
// The values are read by viper from a config file or environment variable.
type Config struct {
	DBHost        string `mapstructure:"DB_HOST"`
	DBUser        string `mapstructure:"DB_USER"`
	DBPassword    string `mapstructure:"DB_PASSWORD"`
	DBName        string `mapstructure:"DB_NAME"`
	DBPort        string `mapstructure:"DB_PORT"`
	ServerAddress string `mapstructure:"SERVER_ADDRESS"`
	PluginsDir    string `mapstructure:"PLUGINS_DIR"`

	// Worker Configurations
	FpingPath                  string `mapstructure:"FPING_PATH"`
	DiscoveryIntervalSeconds   int    `mapstructure:"DISCOVERY_INTERVAL_SECONDS"`
	PollingWorkerConcurrency   int    `mapstructure:"POLLING_WORKER_CONCURRENCY"`
	DiscoveryWorkerConcurrency int    `mapstructure:"DISCOVERY_WORKER_CONCURRENCY"`
	FpingWorkerConcurrency     int    `mapstructure:"FPING_WORKER_CONCURRENCY"`

	// Scheduler Configurations
	SchedulerTickIntervalSeconds int `mapstructure:"SCHEDULER_TICK_INTERVAL_SECONDS"`
	FpingTimeoutMs               int `mapstructure:"FPING_TIMEOUT_MS"`
	FpingRetryCount              int `mapstructure:"FPING_RETRY_COUNT"`

	// Security Configurations
	JWTSecret   string `mapstructure:"JWT_SECRET"`
	TLSCertFile string `mapstructure:"TLS_CERT_FILE"`
	TLSKeyFile  string `mapstructure:"TLS_KEY_FILE"`
}

// LoadConfig reads configuration from file or environment variables.
func LoadConfig(path string) (config Config, err error) {
	viper.AddConfigPath(path)
	viper.SetConfigName("app")
	viper.SetConfigType("yaml")

	// Allow Viper to read Environment Variables
	viper.AutomaticEnv()

	// Map "DB_SOURCE" to "db_source" in files, or handle nested dots
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	err = viper.ReadInConfig()
	if err != nil {
		return
	}

	err = viper.Unmarshal(&config)
	return
}
