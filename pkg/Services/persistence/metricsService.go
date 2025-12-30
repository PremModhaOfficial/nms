package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"nms/pkg/models"
	"nms/pkg/plugin"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// ═══════════════════════════════════════════════════════════════════════════
// JOB TYPES
// ═══════════════════════════════════════════════════════════════════════════

type jobType int

const (
	jobTypeWrite jobType = iota
	jobTypeRead
)

// metricsJob represents a unit of work for the worker pool.
type metricsJob struct {
	jobType jobType

	// For writes (from poller)
	writeResults []plugin.Result

	// For reads (from API)
	readRequest models.Request
}

// ═══════════════════════════════════════════════════════════════════════════
// QUERY TYPES (kept from metricsReader)
// ═══════════════════════════════════════════════════════════════════════════

// pathValidator ensures path segments start with a letter and contain safe chars only.
var pathValidator = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,31}(\.[a-zA-Z][a-zA-Z0-9_]{0,31})*$`)

// MetricResult represents a single point in the time series.
type MetricResult struct {
	Timestamp time.Time       `json:"timestamp"`
	Value     json.RawMessage `json:"value"`
}

// BatchMetricResult groups results by device ID.
type BatchMetricResult struct {
	DeviceID int64           `json:"device_id"`
	Results  []*MetricResult `json:"results"`
}

// MetricQueryRequest holds parameters for a metrics query.
type MetricQueryRequest struct {
	DeviceIDs []int64
	Query     models.MetricQuery
}

// ═══════════════════════════════════════════════════════════════════════════
// METRICS SERVICE
// ═══════════════════════════════════════════════════════════════════════════

// MetricsService handles both metrics writes (from poller) and reads (from API)
// using a worker pool for point-to-point request distribution.
type MetricsService struct {
	// Input channels
	pollResults <-chan []plugin.Result
	queryReqs   <-chan models.Request

	// Separate DB pools for isolation
	writeDB *sql.DB
	readDB  *sql.DB

	// Worker pool
	workerCount int
	jobChan     chan metricsJob

	// Failure events sent to HealthMonitor
	failureChan chan<- models.Event

	// Query defaults
	defaultLimit      int
	defaultRangeHours int
}

// NewMetricsService creates a new unified metrics service.
func NewMetricsService(
	pollResults <-chan []plugin.Result,
	queryReqs <-chan models.Request,
	writeDB *sql.DB,
	readDB *sql.DB,
	workerCount int,
	failureChan chan<- models.Event,
	defaultLimit int,
	defaultRangeHours int,
) *MetricsService {
	return &MetricsService{
		pollResults:       pollResults,
		queryReqs:         queryReqs,
		writeDB:           writeDB,
		readDB:            readDB,
		workerCount:       workerCount,
		jobChan:           make(chan metricsJob, workerCount*2), // buffer = 2x workers
		failureChan:       failureChan,
		defaultLimit:      defaultLimit,
		defaultRangeHours: defaultRangeHours,
	}
}

// Run starts the metrics service.
func (s *MetricsService) Run(ctx context.Context) {
	slog.Info("Starting metrics service", "component", "MetricsService", "workers", s.workerCount)

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < s.workerCount; i++ {
		wg.Add(1)
		go s.worker(ctx, i, &wg)
	}

	// Dispatch loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				close(s.jobChan)
				return
			case results := <-s.pollResults:
				s.jobChan <- metricsJob{
					jobType:      jobTypeWrite,
					writeResults: results,
				}
			case req := <-s.queryReqs:
				s.jobChan <- metricsJob{
					jobType:     jobTypeRead,
					readRequest: req,
				}
			}
		}
	}()

	// Wait for workers to finish
	wg.Wait()
	slog.Info("Stopped metrics service", "component", "MetricsService")
}

// worker processes jobs from the job channel.
func (s *MetricsService) worker(ctx context.Context, id int, wg *sync.WaitGroup) {
	defer wg.Done()
	slog.Debug("Worker started", "component", "MetricsService", "worker_id", id)

	for job := range s.jobChan {
		switch job.jobType {
		case jobTypeWrite:
			s.handleWrite(ctx, job.writeResults)
		case jobTypeRead:
			s.handleQuery(ctx, job.readRequest)
		}
	}

	slog.Debug("Worker stopped", "component", "MetricsService", "worker_id", id)
}

// ═══════════════════════════════════════════════════════════════════════════
// WRITE HANDLING (from MetricsWriter)
// ═══════════════════════════════════════════════════════════════════════════

// handleWrite persists polling metrics using batch insert.
func (s *MetricsService) handleWrite(ctx context.Context, results []plugin.Result) {
	slog.Debug("Processing write job", "component", "MetricsService", "count", len(results))

	// Separate successful results from failures
	rows := make([][]any, 0, len(results))
	now := time.Now()

	for _, result := range results {
		if result.Success {
			rows = append(rows, []any{result.DeviceID, result.Data, now})
		} else {
			slog.Error("Poll result error", "component", "MetricsService",
				"device_id", result.DeviceID, "target", result.Target,
				"port", result.Port, "error", result.Error)
			// Emit failure event to HealthMonitor
			s.failureChan <- models.Event{
				Type: models.EventDeviceFailure,
				Payload: &models.DeviceFailureEvent{
					DeviceID:  result.DeviceID,
					Timestamp: now,
					Reason:    "poll",
				},
			}
		}
	}

	if len(rows) == 0 {
		slog.Debug("No successful results to insert", "component", "MetricsService")
		return
	}

	// Get connection and use pgx.CopyFrom for batch insert
	conn, err := s.writeDB.Conn(ctx)
	if err != nil {
		slog.Error("Failed to get write connection", "component", "MetricsService", "error", err)
		return
	}
	defer conn.Close()

	err = conn.Raw(func(driverConn any) error {
		pgxConn := driverConn.(*stdlib.Conn).Conn()

		_, copyErr := pgxConn.CopyFrom(
			ctx,
			pgx.Identifier{"metrics"},
			[]string{"device_id", "data", "timestamp"},
			pgx.CopyFromRows(rows),
		)
		return copyErr
	})

	if err != nil {
		slog.Error("Batch insert failed", "component", "MetricsService", "error", err)
		return
	}

	slog.Debug("Batch inserted metrics", "component", "MetricsService", "count", len(rows))
}

// ═══════════════════════════════════════════════════════════════════════════
// READ HANDLING (from MetricsReader)
// ═══════════════════════════════════════════════════════════════════════════

// handleQuery handles metrics query requests.
func (s *MetricsService) handleQuery(ctx context.Context, req models.Request) {
	var resp models.Response

	query, ok := req.Payload.(*MetricQueryRequest)
	if !ok {
		resp.Error = fmt.Errorf("invalid payload for metric query")
		req.ReplyCh <- resp
		return
	}

	results, err := s.getMetricsBatch(ctx, query.DeviceIDs, query.Query)
	if err != nil {
		resp.Error = err
	} else {
		resp.Data = results
	}

	req.ReplyCh <- resp
}

// getMetricsBatch fetches metrics for multiple devices.
func (s *MetricsService) getMetricsBatch(ctx context.Context, deviceIDs []int64, query models.MetricQuery) ([]*BatchMetricResult, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = s.defaultLimit
	}

	// Default time range if not provided
	if query.End.IsZero() {
		query.End = time.Now()
	}
	if query.Start.IsZero() {
		query.Start = query.End.Add(-time.Duration(s.defaultRangeHours) * time.Hour)
	}

	// Validate path to prevent SQL injection
	if err := validatePath(query.Path); err != nil {
		return nil, err
	}

	// Convert dot notation to PG JSONB path array format: cpu.total -> {cpu,total}
	pgPath := strings.Replace(query.Path, ".", ",", -1)

	// Build prepared statement with parameterized query
	sqlQuery := fmt.Sprintf(`
		SELECT 
			timestamp, 
			data #> '{%s}' as value 
		FROM metrics 
		WHERE device_id = $1 
		  AND timestamp >= $2 AND timestamp <= $3 
		ORDER BY timestamp DESC 
		LIMIT $4`, pgPath)

	stmt, err := s.readDB.PrepareContext(ctx, sqlQuery)
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

// validatePath checks if the JSONB path is safe to use in queries.
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
