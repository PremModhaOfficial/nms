-- Full Schema for Network Management System (NMS)

-- 1. Credential Profiles
CREATE TABLE IF NOT EXISTS credential_profiles (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL,
    payload TEXT NOT NULL, -- Encrypted data
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- 2. Discovery Profiles
CREATE TABLE IF NOT EXISTS discovery_profiles (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    target TEXT NOT NULL, -- CIDR or IP
    port INTEGER NOT NULL,
    credential_profile_id BIGINT NOT NULL REFERENCES credential_profiles(id),
    auto_provision BOOLEAN DEFAULT false,
    auto_run BOOLEAN DEFAULT false,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- 3. Devices
CREATE TABLE IF NOT EXISTS devices (
    id BIGSERIAL PRIMARY KEY,
    hostname TEXT,
    ip_address INET NOT NULL,
    plugin_id TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 0,
    credential_profile_id BIGINT NOT NULL REFERENCES credential_profiles(id),
    discovery_profile_id BIGINT NOT NULL REFERENCES discovery_profiles(id),
    polling_interval_seconds INTEGER DEFAULT 60,
    should_ping BOOLEAN DEFAULT true,
    status TEXT DEFAULT 'discovered', -- discovered, active, inactive, error
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- 4. Metrics
CREATE TABLE IF NOT EXISTS metrics (
    id BIGSERIAL PRIMARY KEY,
    device_id BIGINT NOT NULL REFERENCES devices(id),
    data JSONB NOT NULL,
    timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_metrics_device_ts ON metrics(device_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_devices_ip ON devices(ip_address);