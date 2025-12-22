package database

import (
	"context"
	"fmt"
	"nms/pkg/models"
	"strings"
	"time"
)

// MetricRepository handles specialized queries for metrics
type MetricRepository struct {
	*GormRepository[models.Metric]
}

func NewMetricRepository(repo *GormRepository[models.Metric]) *MetricRepository {
	return &MetricRepository{repo}
}

func (r *MetricRepository) GetMetrics(ctx context.Context, monitorID int64, query models.MetricQuery) ([]map[string]interface{}, error) {
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

	var results []map[string]interface{}

	// Convert dot notation to PG JSONB path array format: cpu.total -> {cpu,total}
	// The #> operator is the Postgres equivalent of chaining ->'key'->'key'
	// using an array path. It is cleaner for dynamic paths.
	pgPath := strings.Replace(query.Path, ".", ",", -1)

	// Simplified query: Just extract the value at step path
	sql := fmt.Sprintf(`
		SELECT 
			timestamp, 
			data #> '{%s}' as value 
		FROM metrics 
		WHERE monitor_id = ? 
		  AND timestamp >= ? AND timestamp <= ? 
		ORDER BY timestamp DESC 
		LIMIT ?`, pgPath)

	err := r.db.WithContext(ctx).Raw(sql, monitorID, query.Start, query.End, limit).Scan(&results).Error

	return results, err
}
