-- NMS Database Schema
-- Run this to initialize the database (replaces GORM AutoMigrate)

-- Credential Profiles
CREATE TABLE IF NOT EXISTS credential_profiles (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Discovery Profiles
CREATE TABLE IF NOT EXISTS discovery_profiles (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    target TEXT NOT NULL,
    port INT NOT NULL,
    credential_profile_id BIGINT NOT NULL REFERENCES credential_profiles(id),
    auto_provision BOOLEAN DEFAULT FALSE,
    auto_run BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Devices
CREATE TABLE IF NOT EXISTS devices (
    id BIGSERIAL PRIMARY KEY,
    hostname TEXT,
    ip_address INET NOT NULL,
    plugin_id TEXT NOT NULL,
    port INT NOT NULL DEFAULT 0,
    credential_profile_id BIGINT NOT NULL REFERENCES credential_profiles(id),
    discovery_profile_id BIGINT NOT NULL REFERENCES discovery_profiles(id),
    polling_interval_seconds INT DEFAULT 60,
    should_ping BOOLEAN DEFAULT TRUE,
    status TEXT DEFAULT 'discovered',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Metrics
CREATE TABLE IF NOT EXISTS metrics (
    id BIGSERIAL PRIMARY KEY,
    device_id BIGINT NOT NULL,
    data JSONB NOT NULL,
    timestamp TIMESTAMPTZ DEFAULT NOW()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_metrics_device_time ON metrics(device_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_devices_status ON devices(status);