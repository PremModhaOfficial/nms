package api

import (
	"net/http"
	"strconv"

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
	r.GET("", listHandler[T](entityType, encryptionKey, reqCh))
	r.GET("/:id", getHandler[T](entityType, encryptionKey, reqCh))
	r.POST("", createHandler[T](entityType, encryptionKey, reqCh))
	r.PUT("/:id", updateHandler[T](entityType, encryptionKey, reqCh))
	r.DELETE("/:id", deleteHandler(entityType, reqCh))
}

// RegisterMetricsRoute creates metrics query route
func RegisterMetricsRoute(g *gin.RouterGroup, reqCh chan<- models.Request) {
	g.POST("/metrics", metricsHandler(reqCh))
}

// maskCredentialPayload hides sensitive payload data
func maskCredentialPayload(cred *models.CredentialProfile) {
	if cred != nil {
		cred.Payload = "[HIDDEN]"
	}
}

// listHandler returns all entities
func listHandler[T any](entityType string, encryptionKey string, reqCh chan<- models.Request) gin.HandlerFunc {
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

		// Decrypt results
		if items, ok := resp.Data.([]*T); ok {
			decryptedItems := make([]*T, len(items))
			for i, item := range items {
				dec, _ := DecryptStruct(*item, encryptionKey)
				// Mask credentials
				if cred, ok := any(&dec).(*models.CredentialProfile); ok {
					maskCredentialPayload(cred)
				}
				decryptedItems[i] = &dec
			}
			c.JSON(http.StatusOK, decryptedItems)
			return
		}
		c.JSON(http.StatusOK, resp.Data)
	}
}

// getHandler returns a single entity by ID
func getHandler[T any](entityType string, encryptionKey string, reqCh chan<- models.Request) gin.HandlerFunc {
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

		// Decrypt result
		if item, ok := resp.Data.(*T); ok {
			dec, _ := DecryptStruct(*item, encryptionKey)
			// Mask credentials
			if cred, ok := any(&dec).(*models.CredentialProfile); ok {
				maskCredentialPayload(cred)
			}
			c.JSON(http.StatusOK, &dec)
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
		encryptedEntity, err := EncryptStruct(entity, encryptionKey)
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

		// Decrypt for response
		if item, ok := resp.Data.(*T); ok {
			dec, _ := DecryptStruct(*item, encryptionKey)
			c.JSON(http.StatusCreated, &dec)
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
		encryptedEntity, err := EncryptStruct(entity, encryptionKey)
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

		// Decrypt for response
		if item, ok := resp.Data.(*T); ok {
			dec, _ := DecryptStruct(*item, encryptionKey)
			c.JSON(http.StatusOK, &dec)
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
	DeviceIDs []int64 `json:"device_ids" binding:"required"`
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

		if len(req.DeviceIDs) == 0 {
			respondError(c, http.StatusBadRequest, "device_ids is required")
			return
		}

		replyCh := make(chan models.Response, 1)
		reqCh <- models.Request{
			Operation:  models.OpQuery,
			EntityType: "Metric",
			Payload: &persistence.MetricQueryRequest{
				DeviceIDs: req.DeviceIDs,
				Query:     req.MetricQuery,
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
