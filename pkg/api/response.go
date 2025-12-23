package api

import "github.com/gin-gonic/gin"

// respondError sends a structured JSON error response
func respondError(c *gin.Context, code int, message string) {
	c.JSON(code, gin.H{
		"error": gin.H{
			"message": message,
			"status":  code,
		},
	})
	c.Abort()
}
