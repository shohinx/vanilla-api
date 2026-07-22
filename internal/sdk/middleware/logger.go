package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

func Logger(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}

	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.InfoContext(
			c.Request.Context(),
			"http_request",
			"method", c.Request.Method,
			"path", c.FullPath(),
			"raw_path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", float64(time.Since(start).Microseconds())/1000,
			"request_id", c.GetString("request_id"),
			"client_ip", c.ClientIP(),
		)
	}
}
