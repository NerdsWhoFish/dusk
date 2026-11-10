// Package telemetry configures Dusk's OpenTelemetry runtime.
package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Shutdown flushes and stops the telemetry pipeline.
type Shutdown func(context.Context) error

// Start configures OTLP/HTTP trace export when a standard OTLP endpoint is set.
func Start(ctx context.Context, serviceName, serviceVersion string, log *slog.Logger) (Shutdown, error) {
	if !configured() {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
			attribute.String("service.version", serviceVersion),
		),
		resource.WithFromEnv(),
	)
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(privateExporter{SpanExporter: exporter}),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Error("telemetry pipeline", "error", err)
	}))

	return provider.Shutdown, nil
}

// HTTPHandler records server spans and extracts W3C trace context.
func HTTPHandler(next http.Handler, loggers ...*slog.Logger) http.Handler {
	log := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		log = loggers[0]
	}
	return otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics := httpsnoop.CaptureMetrics(next, w, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("http.route", route))
		if route == "GET /healthz" || route == "GET /readyz" {
			return
		}
		level := slog.LevelInfo
		if metrics.Code >= 500 {
			level = slog.LevelError
		}
		log.Log(r.Context(), level, "http request completed",
			"http_route", route, "http_status", metrics.Code, "duration_ms", metrics.Duration.Milliseconds())
	}), "dusk.http")
}

// HTTPClient returns a client that records spans and injects W3C trace context.
func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: otelhttp.NewTransport(handledForbiddenTransport{base: http.DefaultTransport}),
		Timeout:   timeout,
	}
}

// handledForbidden marks a call whose HTTP 403 the caller already treats as
// an outcome rather than a failure.
type handledForbidden struct{}

// HandledForbidden returns a context in which an HTTP 403 recorded by
// [HTTPClient] keeps its status code and is not an error span.
// otelhttp marks every status at or above 400 as failed, which pages for a
// refusal the caller has already decided not to fail on.
func HandledForbidden(ctx context.Context) context.Context {
	return context.WithValue(ctx, handledForbidden{}, true)
}

// handledForbiddenTransport sits under otelhttp. The instrumentation sets
// span status to Error for every HTTP status of 400 or above after the base
// transport returns, and the SDK keeps a status that outranks a later one.
// Ok outranks Error, so setting it here is what that later call cannot undo.
type handledForbiddenTransport struct {
	base http.RoundTripper
}

func (t handledForbiddenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	res, err := base.RoundTrip(r)
	if err != nil || res == nil || res.StatusCode != http.StatusForbidden {
		return res, err
	}
	if r.Context().Value(handledForbidden{}) == nil {
		return res, err
	}
	trace.SpanFromContext(r.Context()).SetStatus(codes.Ok, "")
	return res, nil
}

// LogHandler adds trace and span identifiers to context-aware slog records.
func LogHandler(next slog.Handler) slog.Handler {
	return traceHandler{Handler: next}
}

func configured() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true") {
		return false
	}
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != ""
}

type traceHandler struct {
	slog.Handler
}

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		record.AddAttrs(
			slog.String("trace_id", span.TraceID().String()),
			slog.String("span_id", span.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, record)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{Handler: h.Handler.WithGroup(name)}
}
