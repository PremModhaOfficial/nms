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
	"nms/pkg/plugin"

	"gorm.io/gorm"
)

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

// MetricsService handles all metrics-related database operations.
// This is the high-volume hot path for polling data.
type MetricsService struct {
	pollResults       chan []plugin.Result
	requests          <-chan models.Request
	sqlDB             *sql.DB
	db                *gorm.DB
	defaultLimit      int
	defaultRangeHours int
}

// NewMetricsService creates a new metrics writer service.
func NewMetricsService(
	pollResults chan []plugin.Result,
	requests <-chan models.Request,
	db *gorm.DB,
	defaultLimit int,
	defaultRangeHours int,
) *MetricsService {
	sqlDB, err := db.DB()
	if err != nil {
		// This should theoretically not happen if db is connected
		slog.Error("Failed to get sql.DB from gorm.DB", "component", "MetricsService", "error", err)
	}

	return &MetricsService{
		pollResults:       pollResults,
		requests:          requests,
		sqlDB:             sqlDB,
		db:                db,
		defaultLimit:      defaultLimit,
		defaultRangeHours: defaultRangeHours,
	}
}

// Run starts the metrics writer's main loop.
func (writer *MetricsService) Run(ctx context.Context) {
	slog.Info("Starting metrics writer", "component", "MetricsService")

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping metrics writer", "component", "MetricsService")
			return
		case results := <-writer.pollResults:
			writer.savePollResults(ctx, results)
		case req := <-writer.requests:
			writer.handleQuery(ctx, req)
		}
	}
}

// savePollResults persists polling metrics to the database.
func (writer *MetricsService) savePollResults(ctx context.Context, results []plugin.Result) {
	slog.Debug("Saving poll results", "component", "MetricsService", "count", len(results))

	for _, result := range results {
		if result.Success {
			metric := models.Metric{
				MonitorID: result.MonitorID,
				Data:      result.Data,
			}
			if err := writer.db.WithContext(ctx).Create(&metric).Error; err != nil {
				slog.Error("Error saving metric", "component", "MetricsService", "monitor_id", result.MonitorID, "error", err)
			} else {
				slog.Debug("Saved metric", "component", "MetricsService", "monitor_id", result.MonitorID, "size_bytes", len(result.Data))
			}
		} else {
			slog.Warn("Poll result error", "component", "MetricsService", "target", result.Target, "port", result.Port, "error", result.Error)
		}
	}
}

// handleQuery handles metrics query requests.
func (writer *MetricsService) handleQuery(ctx context.Context, req models.Request) {
	var resp models.Response

	query, ok := req.Payload.(*MetricQueryRequest)
	if !ok {
		resp.Error = fmt.Errorf("invalid payload for metric query")
		req.ReplyCh <- resp
		return
	}

	results, err := writer.getMetricsBatch(ctx, query.MonitorIDs, query.Query)
	if err != nil {
		resp.Error = err
	} else {
		resp.Data = results
	}

	req.ReplyCh <- resp
}

// getMetricsBatch fetches metrics for multiple monitors using a prepared statement.
func (writer *MetricsService) getMetricsBatch(ctx context.Context, monitorIDs []int64, query models.MetricQuery) ([]*BatchMetricResult, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = writer.defaultLimit
	}

	// Default time range if not provided (last 1 hour)
	if query.End.IsZero() {
		query.End = time.Now()
	}
	if query.Start.IsZero() {
		query.Start = query.End.Add(-time.Duration(writer.defaultRangeHours) * time.Hour)
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

	stmt, err := writer.sqlDB.PrepareContext(ctx, sqlQuery)
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

// MetricQueryRequest holds parameters for a metrics query.
type MetricQueryRequest struct {
	MonitorIDs []int64
	Query      models.MetricQuery
}
