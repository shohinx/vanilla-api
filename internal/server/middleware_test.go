package server

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestLoggerIncludesInternalCauseAndRequestContext(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	db := &fakeDatabase{menuErr: errors.New("database connection lost")}
	handler := NewWithLogger(db, &fakeDub{}, nil, Config{}, logger).RegisterRoutes()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/menu", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", response.Code)
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("expected an X-Request-ID response header")
	}
	for _, expected := range []string{
		`"msg":"HTTP request"`,
		`"route":"/api/v1/menu"`,
		`"status":500`,
		"database connection lost",
	} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("log does not contain %q: %s", expected, logs.String())
		}
	}
}

func TestRecoveryMiddlewareReturnsJSONWithoutPropagatingPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	server := &Server{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	router := gin.New()
	router.Use(server.requestLogger(), server.recoverer())
	router.GET("/panic", func(*gin.Context) { panic("boom") })
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("unexpected response body: %s", response.Body.String())
	}
	if !strings.Contains(logs.String(), "panic in HTTP handler: boom") {
		t.Fatalf("panic cause was not logged: %s", logs.String())
	}
}

func TestRequestBodyLimitHandlesKnownAndStreamingBodies(t *testing.T) {
	payload := `{"padding":"` + strings.Repeat("x", int(maxJSONBodyBytes)) + `"}`
	handler := newTestRouter(&fakeDatabase{})

	for _, test := range []struct {
		name      string
		streaming bool
	}{
		{name: "known content length"},
		{name: "streaming body", streaming: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/order-plans/quote", strings.NewReader(payload))
			request.Header.Set("Content-Type", "application/json")
			if test.streaming {
				request.ContentLength = -1
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("expected status 413, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}
