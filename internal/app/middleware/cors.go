package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

func LocalDevCORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allowedOrigin(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-Trace-ID")
			c.Header("Access-Control-Expose-Headers", "X-Trace-ID")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Vary", "Origin")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func allowedOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	if origin == "null" {
		return true
	}
	for _, allowed := range strings.Split(os.Getenv("WIKIOS_CORS_ALLOWED_ORIGINS"), ",") {
		allowed = strings.TrimSpace(allowed)
		if allowed != "" && (allowed == "*" || allowed == origin) {
			return true
		}
	}
	return strings.HasPrefix(origin, "http://localhost:3000") ||
		strings.HasPrefix(origin, "http://127.0.0.1:3000") ||
		strings.HasPrefix(origin, "https://localhost:3000") ||
		strings.HasPrefix(origin, "https://127.0.0.1:3000") ||
		strings.HasPrefix(origin, "http://192.168.") ||
		strings.HasPrefix(origin, "http://10.") ||
		strings.HasPrefix(origin, "http://172.") ||
		strings.HasPrefix(origin, "https://192.168.") ||
		strings.HasPrefix(origin, "https://10.") ||
		strings.HasPrefix(origin, "https://172.")
}
