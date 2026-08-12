//! Environment-driven configuration (Go pkg/config). Values come from
//! environment variables with the defaults below.

use std::env;
use std::time::Duration;

/// All configuration of the application. Env vars with defaults (matching Go).
#[derive(Debug, Clone)]
pub struct Config {
    // Database
    pub db_host: String,
    pub db_user: String,
    pub db_password: String,
    pub db_name: String,
    pub db_port: String,

    // Server
    pub tls_cert_file: String,
    pub tls_key_file: String,
    pub trusted_proxies: String,

    // General
    pub plugins_dir: String,

    // Workers
    pub poll_worker_count: i32,
    pub disc_worker_count: i32,

    // Scheduler
    pub poll_interval_sec: i32,
    pub av_check_timeout_ms: i32,
    pub av_check_retries: i32,

    // Security
    pub jwt_secret: String,
    pub encryption_key: String,
    pub admin_user: String,
    pub admin_hash: String,

    // Authentication
    pub session_duration_hours: i32,

    // Metrics Query Defaults
    pub metrics_default_limit: i32,
    pub metrics_default_lookback_hours: i32,

    // Connection Pool Settings
    pub db_max_open_conns: u32,
    pub db_max_idle_conns: u32,
    pub db_conn_max_life_mins: u64,

    // Health Monitor
    pub failure_window_min: i32,
    pub failure_threshold: i32,

    // Metrics Service Worker Pool
    pub metrics_worker_count: i32,
}

impl Config {
    pub fn poll_interval(&self) -> Duration {
        Duration::from_secs(self.poll_interval_sec as u64)
    }

    pub fn failure_window(&self) -> Duration {
        Duration::from_secs((self.failure_window_min as u64) * 60)
    }

    /// Critical secrets must not use insecure defaults in production.
    pub fn validate_secrets(&self) -> Result<(), String> {
        if self.jwt_secret == "default-insecure-secret-change-me" {
            return Err("JWT_SECRET must be changed from default for production".into());
        }
        if self.encryption_key == "1234567890123456789012345678901212345678901234567890123456789012" {
            return Err("ENCRYPTION_KEY must be changed from default for production".into());
        }
        if self.admin_hash == "$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu" {
            return Err("NMS_ADMIN_HASH must be changed from the default 'admin' password for production".into());
        }
        if self.db_password == "nmslite" {
            return Err("DB_PASSWORD must be changed from the default for production".into());
        }
        if self.db_password.trim().is_empty() {
            return Err("DB_PASSWORD must not be empty for production".into());
        }
        Ok(())
    }
}

/// env reads a string env var or returns the default when unset or empty.
fn env(key: &str, def: &str) -> String {
    match env::var(key) {
        Ok(v) if !v.is_empty() => v,
        _ => def.to_string(),
    }
}

/// env_int reads an int env var or returns the default when unset/unparsable.
fn env_int(key: &str, def: i32) -> i32 {
    match env::var(key) {
        Ok(v) => v.parse::<i32>().unwrap_or(def),
        Err(_) => def,
    }
}

/// LoadConfig reads configuration from environment variables.
pub fn load_config() -> Result<Config, String> {
    let cfg = Config {
        db_host: env("DB_HOST", "localhost"),
        db_user: env("DB_USER", "nmslite"),
        db_password: env("DB_PASSWORD", "nmslite"),
        db_name: env("DB_NAME", "nmslite"),
        db_port: env("DB_PORT", "5432"),
        tls_cert_file: env("TLS_CERT_FILE", ""),
        tls_key_file: env("TLS_KEY_FILE", ""),
        trusted_proxies: env("TRUSTED_PROXIES", ""),
        plugins_dir: env("PLUGINS_DIR", "plugins"),
        poll_worker_count: env_int("POLL_WORKER_COUNT", 5),
        disc_worker_count: env_int("DISC_WORKER_COUNT", 3),
        poll_interval_sec: env_int("POLL_INTERVAL_SEC", 30),
        av_check_timeout_ms: env_int("AV_CHECK_TIMEOUT_MS", 500),
        av_check_retries: env_int("AV_CHECK_RETRIES", 2),
        jwt_secret: env("JWT_SECRET", "default-insecure-secret-change-me"),
        encryption_key: env(
            "ENCRYPTION_KEY",
            "1234567890123456789012345678901212345678901234567890123456789012",
        ),
        admin_user: env("NMS_ADMIN_USER", "admin"),
        admin_hash: env(
            "NMS_ADMIN_HASH",
            "$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu",
        ),
        session_duration_hours: env_int("SESSION_DURATION_HOURS", 168),
        metrics_default_limit: env_int("METRICS_DEFAULT_LIMIT", 100),
        metrics_default_lookback_hours: env_int("METRICS_DEFAULT_LOOKBACK_HOURS", 1),
        db_max_open_conns: env("DB_MAX_OPEN_CONNS", "25").parse().unwrap_or(25),
        db_max_idle_conns: env("DB_MAX_IDLE_CONNS", "10").parse().unwrap_or(10),
        db_conn_max_life_mins: env("DB_CONN_MAX_LIFE_MINS", "30").parse().unwrap_or(30),
        failure_window_min: env_int("FAILURE_WINDOW_MIN", 3),
        failure_threshold: env_int("FAILURE_THRESHOLD", 3),
        metrics_worker_count: env_int("METRICS_WORKER_COUNT", 4),
    };

    // Validate scheduler tick interval (Go: must be at least 20 seconds).
    if cfg.poll_interval_sec < 20 {
        return Err("POLL_INTERVAL_SEC must be at least 20 seconds".into());
    }

    Ok(cfg)
}

/// Find the fping binary in PATH.
pub fn find_fping_path() -> Result<String, String> {
    let path = env::var("PATH").unwrap_or_default();
    for dir in path.split(':') {
        if dir.is_empty() {
            continue;
        }
        let candidate = std::path::Path::new(dir).join("fping");
        if candidate.is_file() {
            return Ok(candidate.to_string_lossy().into_owned());
        }
    }
    Err("fping utility not found. Please install it (e.g., 'sudo apt install fping' or 'brew install fping')".into())
}
