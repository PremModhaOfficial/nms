package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"nms/pkg/models"
)

// pathValidator ensures path segments start with a letter and contain safe chars only.
// Format: segment(.segment)* where segment is [a-zA-Z][a-zA-Z0-9_]{0,31}
var pathValidator = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,31}(\.[a-zA-Z][a-zA-Z0-9_]{0,31})*$`)

// validatePath checks if the JSONB path is safe to use in queries
func validatePath(path string) error {
	if path == "" {
		return nil // Empty path returns full data
	}
	if len(path) > 128 {
		return fmt.Errorf("invalid path: exceeds maximum length of 128 characters")
	}
	if !pathValidator.MatchString(path) {
		return fmt.Errorf("invalid path: must start with a letter and contain only alphanumeric characters, dots, and underscores")
	}
	return nil
}

// MetricResult represents a single point in the time series
type MetricResult struct {
	Timestamp time.Time       `json:"timestamp"`
	Value     json.RawMessage `json:"value"`
}

// BatchMetricResult groups results by device ID
type BatchMetricResult struct {
	DeviceID int64           `json:"device_id"`
	Results  []*MetricResult `json:"results"`
}

// MetricQueryRequest holds parameters for a metrics query.
type MetricQueryRequest struct {
	DeviceIDs []int64
	Query     models.MetricQuery
}

// MetricsReader handles API metric queries.
// It runs in its own goroutine with a dedicated DB pool to avoid
// contention with polling writes.
type MetricsReader struct {
	requests          <-chan models.Request
	sqlDB             *sql.DB
	defaultLimit      int
	defaultRangeHours int
}

// NewMetricsReader creates a new metrics reader.
func NewMetricsReader(
	requests <-chan models.Request,
	sqlDB *sql.DB,
	defaultLimit int,
	defaultRangeHours int,
) *MetricsReader {
	return &MetricsReader{
		requests:          requests,
		sqlDB:             sqlDB,
		defaultLimit:      defaultLimit,
		defaultRangeHours: defaultRangeHours,
	}
}

// Run starts the metrics reader's main loop.
func (reader *MetricsReader) Run(ctx context.Context) {
	slog.Info("Starting metrics reader", "component", "MetricsReader")

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping metrics reader", "component", "MetricsReader")
			return
		case req := <-reader.requests:
			reader.handleQuery(ctx, req)
		}
	}
}

// handleQuery handles metrics query requests.
func (reader *MetricsReader) handleQuery(ctx context.Context, req models.Request) {
	var resp models.Response

	query, ok := req.Payload.(*MetricQueryRequest)
	if !ok {
		resp.Error = fmt.Errorf("invalid payload for metric query")
		req.ReplyCh <- resp
		return
	}

	results, err := reader.getMetricsBatch(ctx, query.DeviceIDs, query.Query)
	if err != nil {
		resp.Error = err
	} else {
		resp.Data = results
	}

	req.ReplyCh <- resp
}

// getMetricsBatch fetches metrics for multiple devices using a prepared statement.
func (reader *MetricsReader) getMetricsBatch(ctx context.Context, deviceIDs []int64, query models.MetricQuery) ([]*BatchMetricResult, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = reader.defaultLimit
	}

	// Default time range if not provided (last 1 hour)
	if query.End.IsZero() {
		query.End = time.Now()
	}
	if query.Start.IsZero() {
		query.Start = query.End.Add(-time.Duration(reader.defaultRangeHours) * time.Hour)
	}

	// Validate path to prevent SQL injection
	if err := validatePath(query.Path); err != nil {
		return nil, err
	}

	// Convert dot notation to PG JSONB path array format: cpu.total -> {cpu,total}
	pgPath := strings.Replace(query.Path, ".", ",", -1)

	// Build prepared statement with parameterized query
	// Note: path is interpolated because PostgreSQL doesn't support parameterized JSONB paths
	sqlQuery := fmt.Sprintf(`
		SELECT 
			timestamp, 
			data #> '{%s}' as value 
		FROM metrics 
		WHERE device_id = $1 
		  AND timestamp >= $2 AND timestamp <= $3 
		ORDER BY timestamp DESC 
		LIMIT $4`, pgPath)

	stmt, err := reader.sqlDB.PrepareContext(ctx, sqlQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	// Build result for each device
	results := make([]*BatchMetricResult, 0, len(deviceIDs))

	for _, deviceID := range deviceIDs {
		rows, err := stmt.QueryContext(ctx, deviceID, query.Start, query.End, limit)
		if err != nil {
			return nil, fmt.Errorf("query failed for device %d: %w", deviceID, err)
		}

		var metricResults []*MetricResult
		for rows.Next() {
			var mr MetricResult
			if err := rows.Scan(&mr.Timestamp, &mr.Value); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan failed: %w", err)
			}
			metricResults = append(metricResults, &mr)
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows iteration error: %w", err)
		}

		results = append(results, &BatchMetricResult{
			DeviceID: deviceID,
			Results:  metricResults,
		})
	}

	return results, nil
}
