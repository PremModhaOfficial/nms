package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"nms/pkg/database"
)

// CrudHandler handles CRUD requests for a generic type
type CrudHandler[T any] struct {
	Repo database.Repository[T]
}

// NewCrudHandler creates a new handler
func NewCrudHandler[T any](repo database.Repository[T]) *CrudHandler[T] {
	return &CrudHandler[T]{Repo: repo}
}

// RegisterRoutes registers the CRUD routes
func (h *CrudHandler[T]) RegisterRoutes(r *gin.RouterGroup, path string) {
	g := r.Group(path)
	{
		g.GET("", h.List)
		g.GET("/:id", h.Get)
		g.POST("", h.Create)
		g.PUT("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}
}

// List returns all records
func (h *CrudHandler[T]) List(c *gin.Context) {
	items, err := h.Repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

// Get returns a single record
func (h *CrudHandler[T]) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	item, err := h.Repo.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "record not found"})
		return
	}
	c.JSON(http.StatusOK, item)
}

	// Create creates a new record
func (h *CrudHandler[T]) Create(c *gin.Context) {
	var entity T
	if err := c.ShouldBindJSON(&entity); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Encrypt sensitive fields if present
	encryptedEntity, err := database.EncryptStruct(entity)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption failed: " + err.Error()})
		return
	}

	created, err := h.Repo.Create(c.Request.Context(), &encryptedEntity)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, created)
}

// Update updates an existing record
func (h *CrudHandler[T]) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var entity T
	if err := c.ShouldBindJSON(&entity); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Encrypt sensitive fields if present
	encryptedEntity, err := database.EncryptStruct(entity)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption failed: " + err.Error()})
		return
	}

	updated, err := h.Repo.Update(c.Request.Context(), id, &encryptedEntity)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// Delete removes a record
func (h *CrudHandler[T]) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := h.Repo.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
