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

// AuthService handles user authentication and JWT operations.
type AuthService struct {
	jwtSecret     []byte
	adminUsername string
	adminPassHash []byte
}

// NewAuthService creates a new AuthService with the provided configuration.
func NewAuthService(cfg *config.Config) *AuthService {
	return &AuthService{
		jwtSecret:     []byte(cfg.JWTSecret),
		adminUsername: cfg.AdminUser,
		adminPassHash: []byte(cfg.AdminHash),
	}
}

// LoginRequest represents the login payload
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginHandler handles user authentication and issues a JWT.
func (s *AuthService) LoginHandler(context *gin.Context) {
	var req LoginRequest
	if err := context.ShouldBindJSON(&req); err != nil {
		respondError(context, http.StatusBadRequest, err.Error())
		return
	}

	// Validate credentials against configured values
	if req.Username != s.adminUsername {
		respondError(context, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Compare password against bcrypt hash
	if err := bcrypt.CompareHashAndPassword(s.adminPassHash, []byte(req.Password)); err != nil {
		respondError(context, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Create the token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": req.Username,
		"iss":      "nms-lite",
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
		"iat":      time.Now().Unix(),
	})

	tokenString, err := token.SignedString(s.jwtSecret)
	if err != nil {
		respondError(context, http.StatusInternalServerError, "failed to sign token")
		return
	}

	context.JSON(http.StatusOK, gin.H{"token": tokenString})
}

// JWTMiddleware validates the Authorization header.
func (s *AuthService) JWTMiddleware() gin.HandlerFunc {
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
			return s.jwtSecret, nil
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
