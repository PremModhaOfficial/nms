package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"nms/pkg/models"
	"regexp"
	"strings"
	"time"
)

// MetricRepository handles specialized queries for metrics using prepared statements
type MetricRepository struct {
	db *sql.DB
}

// pathValidator ensures path only contains safe characters (alphanumeric, dots, underscores)
var pathValidator = regexp.MustCompile(`^[a-zA-Z0-9_.]+$`)

// validatePath checks if the JSONB path is safe to use in queries
func validatePath(path string) error {
	if path == "" {
		return nil // Empty path returns full data
	}
	if !pathValidator.MatchString(path) {
		return fmt.Errorf("invalid path: must contain only alphanumeric characters, dots, and underscores")
	}
	return nil
}

// NewMetricRepository creates a MetricRepository from a GORM DB connection
func NewMetricRepository(db *sql.DB) (*MetricRepository, error) {
	return &MetricRepository{db: db}, nil
}

// MetricResult represents a single point in the time series
type MetricResult struct {
	Timestamp time.Time       `json:"timestamp"`
	Value     json.RawMessage `json:"value"`
}

// BatchMetricResult groups results by monitor ID
type BatchMetricResult struct {
	MonitorID int64           `json:"monitor_id"`
	Results   []*MetricResult `json:"results"`
}

func (metricsRepo *MetricRepository) GetMetricsBatch(ctx context.Context, monitorIDs []int64, query models.MetricQuery) ([]*BatchMetricResult, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}

	// Default time range if not provided (last 1 hour)
	if query.End.IsZero() {
		query.End = time.Now()
	}
	if query.Start.IsZero() {
		query.Start = query.End.Add(-1 * time.Hour)
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
		WHERE monitor_id = $1 
		  AND timestamp >= $2 AND timestamp <= $3 
		ORDER BY timestamp DESC 
		LIMIT $4`, pgPath)

	stmt, err := metricsRepo.db.PrepareContext(ctx, sqlQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	// Build result for each monitor
	results := make([]*BatchMetricResult, 0, len(monitorIDs))

	for _, monitorID := range monitorIDs {
		rows, err := stmt.QueryContext(ctx, monitorID, query.Start, query.End, limit)
		if err != nil {
			return nil, fmt.Errorf("query failed for monitor %d: %w", monitorID, err)
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
			MonitorID: monitorID,
			Results:   metricResults,
		})
	}

	return results, nil
}
