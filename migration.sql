-- Drop existing metrics table if it exists
DROP TABLE IF EXISTS metrics;

-- Create the new metrics table with a JSONB column for raw hierarchical data
CREATE TABLE metrics (
    id BIGSERIAL PRIMARY KEY,
    monitor_id BIGINT NOT NULL REFERENCES monitors(id),
    data JSONB NOT NULL,
    timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- Index for efficient querying by monitor and time
CREATE INDEX idx_metrics_monitor_ts ON metrics(monitor_id, timestamp DESC);
