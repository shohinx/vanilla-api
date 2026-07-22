package observability

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/shohinx/vanilla-api/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/shohinx/vanilla-api/internal/sdk/observability"

func SetupTracing(ctx context.Context, cfg config.Observability, logger *slog.Logger) (func(context.Context) error, error) {
	if logger == nil {
		logger = slog.Default()
	}
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if cfg.OTLPHTTPEndpoint == "" {
		logger.InfoContext(ctx, "otel exporter disabled", "reason", "OTLP_HTTP_ENDPOINT is not configured")
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx, otlpHTTPOptions(cfg.OTLPHTTPEndpoint)...)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", cfg.ServiceName)))
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)

	return provider.Shutdown, nil
}

// Tracing creates one server span per HTTP request and extracts any upstream
// W3C trace context from the request headers.
func Tracing() gin.HandlerFunc {
	tracer := otel.Tracer(instrumentationName)
	return func(c *gin.Context) {
		ctx := otel.GetTextMapPropagator().Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		ctx, span := tracer.Start(
			ctx,
			c.Request.Method+" "+c.Request.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", c.Request.Method)),
		)
		defer span.End()
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		span.SetName(c.Request.Method + " " + route)
		span.SetAttributes(
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", status),
		)
		if status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
	}
}

func otlpHTTPOptions(endpoint string) []otlptracehttp.Option {
	endpoint = strings.TrimSpace(endpoint)

	u, err := url.Parse(endpoint)
	if err == nil && u.Scheme != "" && u.Host != "" {
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(u.Host)}
		if u.Path != "" && u.Path != "/" {
			opts = append(opts, otlptracehttp.WithURLPath(u.Path))
		}
		if u.Scheme == "http" {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return opts
	}

	return []otlptracehttp.Option{otlptracehttp.WithEndpoint(endpoint)}
}
