package persistence

import (
	"context"
	"fmt"
	"log/slog"

	"nms/pkg/database"
	"nms/pkg/models"
	"nms/pkg/plugin"

	"gorm.io/gorm"
)

// MetricsService handles all metrics-related database operations.
// This is the high-volume hot path for polling data.
type MetricsService struct {
	pollResults chan []plugin.Result
	requests    <-chan models.Request
	metricRepo  *database.MetricRepository
	db          *gorm.DB
}

// NewMetricsService creates a new metrics writer service.
func NewMetricsService(
	pollResults chan []plugin.Result,
	requests <-chan models.Request,
	metricRepo *database.MetricRepository,
	db *gorm.DB,
) *MetricsService {
	return &MetricsService{
		pollResults: pollResults,
		requests:    requests,
		metricRepo:  metricRepo,
		db:          db,
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

	results, err := writer.metricRepo.GetMetricsBatch(ctx, query.MonitorIDs, query.Query)
	if err != nil {
		resp.Error = err
	} else {
		resp.Data = results
	}

	req.ReplyCh <- resp
}

// MetricQueryRequest holds parameters for a metrics query.
type MetricQueryRequest struct {
	MonitorIDs []int64
	Query      models.MetricQuery
}
