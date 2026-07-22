package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type recordingExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *recordingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (*recordingExporter) Shutdown(context.Context) error { return nil }

func TestTracingRecordsRouteAndErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	exporter := new(recordingExporter)
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})

	router := gin.New()
	router.Use(Tracing())
	router.GET("/users/:id", func(c *gin.Context) {
		c.Status(http.StatusInternalServerError)
	})
	request := httptest.NewRequest(http.MethodGet, "/users/123", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	if len(exporter.spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(exporter.spans))
	}
	span := exporter.spans[0]
	if span.Name() != "GET /users/:id" {
		t.Fatalf("span name = %q", span.Name())
	}
	if span.Status().Code != codes.Error {
		t.Fatalf("span status = %v, want error", span.Status().Code)
	}
	if got := span.Parent().SpanID().String(); got != "00f067aa0ba902b7" {
		t.Fatalf("parent span ID = %q", got)
	}
}
