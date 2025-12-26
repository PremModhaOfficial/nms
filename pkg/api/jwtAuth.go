package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"nms/pkg/config"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/crypto/bcrypt"
)

// JwtAuth handles user authentication and JWT operations.
type JwtAuth struct {
	jwtSecret     []byte
	adminUsername string
	adminPassHash []byte
	expiryHours   int
}

// Auth creates a new JwtAuth with the provided configuration.
func Auth(cfg *config.Config) *JwtAuth {
	return &JwtAuth{
		jwtSecret:     []byte(cfg.JWTSecret),
		adminUsername: cfg.AdminUser,
		adminPassHash: []byte(cfg.AdminHash),
		expiryHours:   cfg.SessionDurationHours,
	}
}

// LoginRequest represents the login payload
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginHandler handles user authentication and issues a JWT.
func (jwtAuth *JwtAuth) LoginHandler(context *gin.Context) {
	var req LoginRequest
	if err := context.ShouldBindJSON(&req); err != nil {
		respondError(context, http.StatusBadRequest, err.Error())
		return
	}

	// Validate credentials against configured values
	if req.Username != jwtAuth.adminUsername {
		respondError(context, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Compare password against bcrypt hash
	if err := bcrypt.CompareHashAndPassword(jwtAuth.adminPassHash, []byte(req.Password)); err != nil {
		respondError(context, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Create the token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": req.Username,
		"iss":      "nms-lite",
		"exp":      time.Now().Add(time.Duration(jwtAuth.expiryHours) * time.Hour).Unix(),
		"iat":      time.Now().Unix(),
	})

	tokenString, err := token.SignedString(jwtAuth.jwtSecret)
	if err != nil {
		respondError(context, http.StatusInternalServerError, "failed to sign token")
		return
	}

	context.JSON(http.StatusOK, gin.H{"token": tokenString})
}

// JWTMiddleware validates the Authorization header.
func (jwtAuth *JwtAuth) JWTMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			respondError(c, http.StatusUnauthorized, "authorization header required")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			respondError(c, http.StatusUnauthorized, "invalid authorization header format")
			return
		}

		tokenString := parts[1]
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return jwtAuth.jwtSecret, nil
		})

		if err != nil || !token.Valid {
			respondError(c, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		// Store claims in context for later use
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			c.Set("username", claims["username"])
		}

		c.Next()
	}
}

// SecurityHeaders returns a middleware that sets security headers
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Next()
	}
}
