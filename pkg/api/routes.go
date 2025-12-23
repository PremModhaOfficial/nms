package api

import (
	"net/http"
	"strconv"

	"nms/pkg/database"
	"nms/pkg/models"
	"nms/pkg/persistence"

	"github.com/gin-gonic/gin"
)

// RegisterEntityRoutes creates CRUD routes for any entity type
func RegisterEntityRoutes[T any](
	g *gin.RouterGroup,
	path string,
	entityType string,
	encryptionKey string,
	reqCh chan<- models.Request,
) {
	r := g.Group(path)
	r.GET("", listHandler(entityType, reqCh))
	r.GET("/:id", getHandler(entityType, reqCh))
	r.POST("", createHandler[T](entityType, encryptionKey, reqCh))
	r.PUT("/:id", updateHandler[T](entityType, encryptionKey, reqCh))
	r.DELETE("/:id", deleteHandler(entityType, reqCh))
}

// RegisterMetricsRoute creates metrics query route
func RegisterMetricsRoute(g *gin.RouterGroup, reqCh chan<- models.Request) {
	g.POST("/metrics", metricsHandler(reqCh))
}

// listHandler returns all entities
func listHandler(entityType string, reqCh chan<- models.Request) gin.HandlerFunc {
	return func(c *gin.Context) {
		replyCh := make(chan models.Response, 1)
		reqCh <- models.Request{
			Operation:  models.OpList,
			EntityType: entityType,
			ReplyCh:    replyCh,
		}

		resp := <-replyCh
		if resp.Error != nil {
			respondError(c, http.StatusInternalServerError, resp.Error.Error())
			return
		}
		c.JSON(http.StatusOK, resp.Data)
	}
}

// getHandler returns a single entity by ID
func getHandler(entityType string, reqCh chan<- models.Request) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			respondError(c, http.StatusBadRequest, "invalid id")
			return
		}

		replyCh := make(chan models.Response, 1)
		reqCh <- models.Request{
			Operation:  models.OpGet,
			EntityType: entityType,
			ID:         id,
			ReplyCh:    replyCh,
		}

		resp := <-replyCh
		if resp.Error != nil {
			respondError(c, http.StatusNotFound, "record not found")
			return
		}
		c.JSON(http.StatusOK, resp.Data)
	}
}

// createHandler creates a new entity
func createHandler[T any](entityType string, encryptionKey string, reqCh chan<- models.Request) gin.HandlerFunc {
	return func(c *gin.Context) {
		var entity T
		if err := c.ShouldBindJSON(&entity); err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}

		// Encrypt sensitive fields if present
		encryptedEntity, err := database.EncryptStruct(entity, encryptionKey)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "encryption failed: "+err.Error())
			return
		}

		replyCh := make(chan models.Response, 1)
		reqCh <- models.Request{
			Operation:  models.OpCreate,
			EntityType: entityType,
			Payload:    &encryptedEntity,
			ReplyCh:    replyCh,
		}

		resp := <-replyCh
		if resp.Error != nil {
			respondError(c, http.StatusInternalServerError, resp.Error.Error())
			return
		}
		c.JSON(http.StatusCreated, resp.Data)
	}
}

// updateHandler updates an existing entity
func updateHandler[T any](entityType string, encryptionKey string, reqCh chan<- models.Request) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			respondError(c, http.StatusBadRequest, "invalid id")
			return
		}

		var entity T
		if err := c.ShouldBindJSON(&entity); err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}

		// Encrypt sensitive fields if present
		encryptedEntity, err := database.EncryptStruct(entity, encryptionKey)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "encryption failed: "+err.Error())
			return
		}

		replyCh := make(chan models.Response, 1)
		reqCh <- models.Request{
			Operation:  models.OpUpdate,
			EntityType: entityType,
			ID:         id,
			Payload:    &encryptedEntity,
			ReplyCh:    replyCh,
		}

		resp := <-replyCh
		if resp.Error != nil {
			respondError(c, http.StatusInternalServerError, resp.Error.Error())
			return
		}
		c.JSON(http.StatusOK, resp.Data)
	}
}

// deleteHandler removes an entity
func deleteHandler(entityType string, reqCh chan<- models.Request) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			respondError(c, http.StatusBadRequest, "invalid id")
			return
		}

		replyCh := make(chan models.Response, 1)
		reqCh <- models.Request{
			Operation:  models.OpDelete,
			EntityType: entityType,
			ID:         id,
			ReplyCh:    replyCh,
		}

		resp := <-replyCh
		if resp.Error != nil {
			respondError(c, http.StatusInternalServerError, resp.Error.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "deleted"})
	}
}

// BatchMetricQuery represents a batch query for metrics
type BatchMetricQuery struct {
	MonitorIDs []int64 `json:"monitor_ids" binding:"required"`
	models.MetricQuery
}

// metricsHandler handles metrics queries
func metricsHandler(reqCh chan<- models.Request) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req BatchMetricQuery
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}

		if len(req.MonitorIDs) == 0 {
			respondError(c, http.StatusBadRequest, "monitor_ids is required")
			return
		}

		replyCh := make(chan models.Response, 1)
		reqCh <- models.Request{
			Operation:  models.OpQuery,
			EntityType: "Metric",
			Payload: &persistence.MetricQueryRequest{
				MonitorIDs: req.MonitorIDs,
				Query:      req.MetricQuery,
			},
			ReplyCh: replyCh,
		}

		resp := <-replyCh
		if resp.Error != nil {
			respondError(c, http.StatusInternalServerError, resp.Error.Error())
			return
		}
		c.JSON(http.StatusOK, resp.Data)
	}
}
