package middleware

import (
	"log/slog"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/shohinx/vanilla-api/internal/sdk/errs"
)

func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		if logger != nil {
			logger.ErrorContext(c.Request.Context(), "panic recovered",
				"panic", recovered,
				"request_id", c.GetString("request_id"),
				"stack", string(debug.Stack()),
			)
		}

		c.AbortWithStatusJSON(errs.ErrInternal.Status, gin.H{
			"error": errs.ErrInternal.Key,
		})
	})
}
