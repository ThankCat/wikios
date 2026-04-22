package middleware

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Printf("%s %s %d %s trace=%s", c.Request.Method, c.Request.URL.Path, c.Writer.Status(), time.Since(start), c.GetString("trace_id"))
	}
}
