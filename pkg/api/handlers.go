package api

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"nms/pkg/database"
	"nms/pkg/models"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/crypto/bcrypt"
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

// MetricHandler handles metric related requests
type MetricHandler struct {
	Repo *database.MetricRepository
}

func NewMetricHandler(repo *database.MetricRepository) *MetricHandler {
	return &MetricHandler{Repo: repo}
}

func (h *MetricHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/metrics/:monitor_id", h.Query)
}

func (h *MetricHandler) Query(c *gin.Context) {
	monitorID, err := strconv.ParseInt(c.Param("monitor_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid monitor_id"})
		return
	}

	var req models.MetricQuery
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	results, err := h.Repo.GetMetrics(c.Request.Context(), monitorID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, results)
}

// ══════════════════════════════════════════════════════════════════════════════
// JWT Authentication
// ══════════════════════════════════════════════════════════════════════════════

var (
	jwtSecret     []byte
	adminUsername string
	adminPassHash []byte
)

func init() {
	// Read admin credentials from environment variables
	adminUsername = os.Getenv("NMS_ADMIN_USER")
	if adminUsername == "" {
		adminUsername = "admin"
		log.Println("WARNING: NMS_ADMIN_USER not set. Using default 'admin'.")
	}

	// NMS_ADMIN_HASH should be a bcrypt hash of the password.
	// Generate with: htpasswd -bnBC 10 "" <password> | tr -d ':\n'
	// Or in Go: bcrypt.GenerateFromPassword([]byte("password"), bcrypt.DefaultCost)
	hashStr := os.Getenv("NMS_ADMIN_HASH")
	if hashStr == "" {
		// Development fallback: hash of "admin"
		// DO NOT USE IN PRODUCTION - set NMS_ADMIN_HASH instead
		hashStr = "$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu" // bcrypt hash of "admin"
		log.Println("WARNING: NMS_ADMIN_HASH not set. Using insecure default.")
	}
	adminPassHash = []byte(hashStr)
}

// SetJWTSecret must be called at startup to configure the secret.
func SetJWTSecret(secret string) {
	jwtSecret = []byte(secret)
}

// LoginRequest represents the login payload
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginHandler handles user authentication and issues a JWT.
func LoginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate credentials against environment-configured values
	if req.Username != adminUsername {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	// Compare password against bcrypt hash
	if err := bcrypt.CompareHashAndPassword(adminPassHash, []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	// Create the token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": req.Username,
		"iss":      "nms-lite",
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
		"iat":      time.Now().Unix(),
	})

	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to sign token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": tokenString})
}

// JWTMiddleware validates the Authorization header.
func JWTMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authorization header required"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header format"})
			return
		}

		tokenString := parts[1]
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		// Store claims in context for later use
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			c.Set("username", claims["username"])
		}

		c.Next()
	}
}
