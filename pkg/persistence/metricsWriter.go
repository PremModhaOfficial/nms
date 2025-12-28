package persistence

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"nms/pkg/plugin"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// MetricsWriter handles high-volume polling result ingestion.
// It runs in its own goroutine with a dedicated DB pool to avoid
// contention with API queries.
type MetricsWriter struct {
	pollResults chan []plugin.Result
	sqlDB       *sql.DB
}

// NewMetricsWriter creates a new metrics writer.
func NewMetricsWriter(
	pollResults chan []plugin.Result,
	sqlDB *sql.DB,
) *MetricsWriter {
	return &MetricsWriter{
		pollResults: pollResults,
		sqlDB:       sqlDB,
	}
}

// Run starts the metrics writer's main loop.
func (writer *MetricsWriter) Run(ctx context.Context) {
	slog.Info("Starting metrics writer", "component", "MetricsWriter")

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping metrics writer", "component", "MetricsWriter")
			return
		case results := <-writer.pollResults:
			writer.savePollResults(ctx, results)
		}
	}
}

// savePollResults persists polling metrics to the database using batch insert.
func (writer *MetricsWriter) savePollResults(ctx context.Context, results []plugin.Result) {
	slog.Debug("Saving poll results", "component", "MetricsWriter", "count", len(results))

	// Separate successful results from failures
	rows := make([][]any, 0, len(results))
	now := time.Now()

	for _, result := range results {
		if result.Success {
			rows = append(rows, []any{result.DeviceID, result.Data, now})
		} else {
			slog.Error("Poll result error", "component", "MetricsWriter", "target", result.Target, "port", result.Port, "error", result.Error)
		}
	}

	if len(rows) == 0 {
		slog.Debug("No successful results to insert", "component", "MetricsWriter")
		return
	}

	// Get a connection from the pool and unwrap to pgx.Conn
	conn, err := writer.sqlDB.Conn(ctx)
	if err != nil {
		slog.Error("Failed to get connection", "component", "MetricsWriter", "error", err)
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
		slog.Error("Batch insert failed", "component", "MetricsWriter", "error", err)
		return
	}

	slog.Debug("Batch inserted metrics", "component", "MetricsWriter", "count", len(rows))
}
