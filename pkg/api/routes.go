package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nms/pkg/Services/persistence"
	"nms/pkg/models"

	"github.com/jackc/pgx/v5/pgconn"
)

// rpcTimeout bounds every request-reply channel RPC so a stalled or stopped
// service cannot hang an HTTP handler or leak goroutines.
var rpcTimeout = 5 * time.Second

// errServiceUnavailable is returned by doRequest when the request-reply RPC
// cannot complete within rpcTimeout.
var errServiceUnavailable = errors.New("service unavailable")

// Limits on client-controlled batch sizes (Tiger: explicit bounds at the boundary).
const (
	maxBatchDeviceIDs = 500
	maxMetricLimit    = 1000
)

// RegisterEntityRoutes creates CRUD routes for any entity type. Patterns are
// registered with the provided function so callers can wrap them in auth.
func RegisterEntityRoutes[T any](
	register func(pattern string, h http.Handler),
	path string,
	entityType string,
	encryptionKey string,
	reqCh chan<- models.Request,
) {
	register("GET "+path, listHandler[T](entityType, encryptionKey, reqCh))
	register("GET "+path+"/{id}", getHandler[T](entityType, encryptionKey, reqCh))
	register("POST "+path, createHandler[T](entityType, encryptionKey, reqCh))
	register("PUT "+path+"/{id}", updateHandler[T](entityType, encryptionKey, reqCh))
	register("DELETE "+path+"/{id}", deleteHandler(entityType, reqCh))
}

// RegisterMetricsRoute creates metrics query route.
func RegisterMetricsRoute(register func(pattern string, h http.Handler), path string, reqCh chan<- models.Request) {
	register("POST "+path, metricsHandler(reqCh))
}

// jsonDecode decodes a JSON request body into v, returning a client-safe error.
func jsonDecode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

// doRequest performs a blocking request-reply RPC with a context and deadline.
// The reply channel is buffered (cap 1) so the service side never blocks even
// if the HTTP handler has already given up waiting.
func doRequest(r *http.Request, reqCh chan<- models.Request, req models.Request) (models.Response, error) {
	ctx := r.Context()

	// Carry the HTTP span context on the request so the consuming service
	// continues this request's trace across the reply-channel boundary.
	models.StampRequest(ctx, &req)

	select {
	case reqCh <- req:
	case <-ctx.Done():
		return models.Response{}, ctx.Err()
	case <-time.After(rpcTimeout):
		return models.Response{}, errServiceUnavailable
	}

	select {
	case resp := <-req.ReplyCh:
		return resp, nil
	case <-ctx.Done():
		return models.Response{}, ctx.Err()
	case <-time.After(rpcTimeout):
		return models.Response{}, errServiceUnavailable
	}
}

// respondRPCError maps request-reply RPC transport failures to HTTP responses.
func respondRPCError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		// Client disconnected; nothing to write.
	case errors.Is(err, errServiceUnavailable):
		respondError(w, http.StatusServiceUnavailable, "service temporarily unavailable")
	default:
		respondError(w, http.StatusInternalServerError, "internal server error")
	}
}

// classifyError maps a service-layer error to an HTTP status and a client-safe
// message. The full error is logged by the caller; raw DB internals are never
// forwarded to the client.
func classifyError(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	msg := err.Error()
	switch {
	case errors.Is(err, sql.ErrNoRows), msg == "record not found":
		return http.StatusNotFound, "record not found"
	case strings.Contains(msg, "duplicate key"), strings.Contains(msg, "unique constraint"):
		return http.StatusConflict, "record already exists"
	case isForeignKeyViolation(err):
		// Deleting an entity still referenced by another row (SQLSTATE 23503)
		// is a client error, not a server failure.
		return http.StatusConflict, "record is in use by another resource"
	case strings.Contains(msg, "name cannot be empty"),
		strings.Contains(msg, "target cannot be empty"),
		strings.Contains(msg, "credential_profile_id is required"),
		strings.Contains(msg, "discovery_profile_id is required"),
		strings.Contains(msg, "ip_address is required"),
		strings.Contains(msg, "plugin_id is required"),
		strings.Contains(msg, "immutable after creation"),
		strings.Contains(msg, "payload is required on create"),
		strings.Contains(msg, "port must be between"),
		strings.Contains(msg, "polling_interval_seconds must be between"),
		strings.Contains(msg, "invalid status"),
		strings.Contains(msg, "must be a valid IP address"):
		return http.StatusBadRequest, msg
	default:
		return http.StatusInternalServerError, "internal server error"
	}
}

// isForeignKeyViolation reports whether err is a Postgres foreign-key
// violation (SQLSTATE 23503).
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return true
	}
	return strings.Contains(err.Error(), "foreign key constraint")
}

// respondServiceError logs the real error server-side and sends a sanitized,
// correctly-classified response to the client.
func respondServiceError(w http.ResponseWriter, operation, entityType string, err error) {
	slog.Error("API request failed", "operation", operation, "entity_type", entityType, "error", err)
	code, msg := classifyError(err)
	respondError(w, code, msg)
}

// maskCredential hides the credential payload from API responses. The stored
// value is ciphertext; clients only ever see the sentinel.
func maskCredential[T any](entity *T) {
	if cred, ok := any(entity).(*models.CredentialProfile); ok {
		cred.Payload = "[HIDDEN]"
	}
}

// listHandler returns all entities
func listHandler[T any](entityType string, encryptionKey string, reqCh chan<- models.Request) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := doRequest(r, reqCh, models.Request{
			Operation:  models.OpList,
			EntityType: entityType,
			ReplyCh:    make(chan models.Response, 1),
		})
		if err != nil {
			respondRPCError(w, err)
			return
		}
		if resp.Error != nil {
			respondServiceError(w, models.OpList, entityType, resp.Error)
			return
		}

		// Mask credentials; payloads are stored encrypted and never served.
		if items, ok := resp.Data.([]*T); ok {
			for _, item := range items {
				maskCredential(item)
			}
			writeJSON(w, http.StatusOK, items)
			return
		}
		writeJSON(w, http.StatusOK, resp.Data)
	}
}

// getHandler returns a single entity by ID
func getHandler[T any](entityType string, encryptionKey string, reqCh chan<- models.Request) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid id")
			return
		}

		resp, err := doRequest(r, reqCh, models.Request{
			Operation:  models.OpGet,
			EntityType: entityType,
			ID:         id,
			ReplyCh:    make(chan models.Response, 1),
		})
		if err != nil {
			respondRPCError(w, err)
			return
		}
		if resp.Error != nil {
			respondServiceError(w, models.OpGet, entityType, resp.Error)
			return
		}

		// Mask credentials; payloads are stored encrypted and never served.
		if item, ok := resp.Data.(*T); ok {
			maskCredential(item)
			writeJSON(w, http.StatusOK, item)
			return
		}
		writeJSON(w, http.StatusOK, resp.Data)
	}
}

// createHandler creates a new entity
func createHandler[T any](entityType string, encryptionKey string, reqCh chan<- models.Request) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var entity T
		if err := jsonDecode(r, &entity); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Encrypt sensitive fields if present
		encryptedEntity, err := EncryptStruct(entity, encryptionKey)
		if err != nil {
			slog.Error("Encryption failed", "operation", models.OpCreate, "entity_type", entityType, "error", err)
			respondError(w, http.StatusInternalServerError, "encryption failed")
			return
		}

		resp, err := doRequest(r, reqCh, models.Request{
			Operation:  models.OpCreate,
			EntityType: entityType,
			Payload:    &encryptedEntity,
			ReplyCh:    make(chan models.Response, 1),
		})
		if err != nil {
			respondRPCError(w, err)
			return
		}
		if resp.Error != nil {
			respondServiceError(w, models.OpCreate, entityType, resp.Error)
			return
		}

		// Mask credentials; payloads are stored encrypted and never served.
		if item, ok := resp.Data.(*T); ok {
			maskCredential(item)
			writeJSON(w, http.StatusCreated, item)
			return
		}
		writeJSON(w, http.StatusCreated, resp.Data)
	}
}

// updateHandler updates an existing entity
func updateHandler[T any](entityType string, encryptionKey string, reqCh chan<- models.Request) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid id")
			return
		}

		var entity T
		if err := jsonDecode(r, &entity); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}

		// For credentials, GET/list responses mask the payload as "[HIDDEN]". A
		// read-modify-write that echoes that sentinel back must NOT overwrite the
		// stored ciphertext. Blank the payload so the repository's
		// update:"omitempty" skips the column and the existing value is kept.
		if entityType == "CredentialProfile" {
			if cp, ok := any(&entity).(*models.CredentialProfile); ok && (cp.Payload == "" || cp.Payload == "[HIDDEN]") {
				cp.Payload = ""
			}
		}

		// EncryptStruct skips empty strings, so a blank payload flows through
		// untouched and update:"omitempty" keeps the stored ciphertext.
		encryptedEntity, err := EncryptStruct(entity, encryptionKey)
		if err != nil {
			slog.Error("Encryption failed", "operation", models.OpUpdate, "entity_type", entityType, "error", err)
			respondError(w, http.StatusInternalServerError, "encryption failed")
			return
		}

		resp, err := doRequest(r, reqCh, models.Request{
			Operation:  models.OpUpdate,
			EntityType: entityType,
			ID:         id,
			Payload:    &encryptedEntity,
			ReplyCh:    make(chan models.Response, 1),
		})
		if err != nil {
			respondRPCError(w, err)
			return
		}
		if resp.Error != nil {
			respondServiceError(w, models.OpUpdate, entityType, resp.Error)
			return
		}

		// Mask credentials; payloads are stored encrypted and never served.
		if item, ok := resp.Data.(*T); ok {
			maskCredential(item)
			writeJSON(w, http.StatusOK, item)
			return
		}
		writeJSON(w, http.StatusOK, resp.Data)
	}
}

// deleteHandler removes an entity
func deleteHandler(entityType string, reqCh chan<- models.Request) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid id")
			return
		}

		resp, err := doRequest(r, reqCh, models.Request{
			Operation:  models.OpDelete,
			EntityType: entityType,
			ID:         id,
			ReplyCh:    make(chan models.Response, 1),
		})
		if err != nil {
			respondRPCError(w, err)
			return
		}
		if resp.Error != nil {
			respondServiceError(w, models.OpDelete, entityType, resp.Error)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": "deleted"})
	}
}

// BatchMetricQuery represents a batch query for metrics
type BatchMetricQuery struct {
	DeviceIDs []int64 `json:"device_ids" binding:"required"`
	models.MetricQuery
}

// metricsHandler handles metrics queries
func metricsHandler(reqCh chan<- models.Request) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req BatchMetricQuery
		if err := jsonDecode(r, &req); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if len(req.DeviceIDs) == 0 {
			respondError(w, http.StatusBadRequest, "device_ids is required")
			return
		}
		if len(req.DeviceIDs) > maxBatchDeviceIDs {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("device_ids exceeds maximum of %d", maxBatchDeviceIDs))
			return
		}
		if req.Limit > maxMetricLimit {
			req.Limit = maxMetricLimit
		}

		resp, err := doRequest(r, reqCh, models.Request{
			Operation:  models.OpQuery,
			EntityType: "Metric",
			Payload: &persistence.MetricQueryRequest{
				DeviceIDs: req.DeviceIDs,
				Query:     req.MetricQuery,
			},
			ReplyCh: make(chan models.Response, 1),
		})
		if err != nil {
			respondRPCError(w, err)
			return
		}
		if resp.Error != nil {
			respondServiceError(w, models.OpQuery, "Metric", resp.Error)
			return
		}
		writeJSON(w, http.StatusOK, resp.Data)
	}
}
