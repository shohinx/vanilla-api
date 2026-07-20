package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

type requestIDContextKey struct{}

var requestSequence atomic.Uint64

const maxJSONBodyBytes int64 = 1 << 20

func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		requestID := newRequestID()
		c.Header("X-Request-ID", requestID)
		ctx := context.WithValue(c.Request.Context(), requestIDContextKey{}, requestID)
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		status := c.Writer.Status()
		requestErr := joinedGinErrors(c)
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError || requestErr != nil {
			level = slog.LevelError
		} else if status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
		attributes := []slog.Attr{
			slog.String("request_id", requestID),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
			slog.Int("response_bytes", c.Writer.Size()),
		}
		if route := c.FullPath(); route != "" {
			attributes = append(attributes, slog.String("route", route))
		}
		if requestErr != nil {
			attributes = append(attributes, slog.Any("error", requestErr))
		}
		s.logger.LogAttrs(ctx, level, "HTTP request", attributes...)
	}
}

func (s *Server) recoverer() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			var err error
			switch value := recovered.(type) {
			case error:
				err = fmt.Errorf("panic in HTTP handler: %w", value)
			default:
				err = fmt.Errorf("panic in HTTP handler: %v", value)
			}
			_ = c.Error(err)
			if !c.Writer.Written() {
				writeError(c, http.StatusInternalServerError, "internal_error", "an unexpected error occurred", nil)
			}
			c.Abort()
		}()
		c.Next()
	}
}

func requestBodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost && c.Request.Method != http.MethodPatch {
			c.Next()
			return
		}
		if c.Request.URL.Path == "/api/v1/admin/images" {
			c.Next()
			return
		}
		if c.Request.ContentLength > maxJSONBodyBytes {
			writeError(c, http.StatusRequestEntityTooLarge, "request_too_large", "request body may not exceed 1 MiB", nil)
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxJSONBodyBytes)
		c.Next()
	}
}

func newRequestID() string {
	sequence := requestSequence.Add(1)
	return strconv.FormatInt(time.Now().UTC().UnixMilli(), 36) + "-" + strconv.FormatUint(sequence, 36)
}

func joinedGinErrors(c *gin.Context) error {
	causes := make([]error, 0, len(c.Errors))
	for _, ginError := range c.Errors {
		causes = append(causes, ginError.Err)
	}
	return errors.Join(causes...)
}

func reportError(c *gin.Context, err error) {
	if err != nil {
		_ = c.Error(err)
	}
}
