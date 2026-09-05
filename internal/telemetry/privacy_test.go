package telemetry

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestHTTPTraceAndLogKeepCorrelationWithoutPrivateRequestData(t *testing.T) {
	const private = "customer@example.com"
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(privateExporter{SpanExporter: exporter}))
	previous, propagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		otel.SetTextMapPropagator(propagator)
		_ = provider.Shutdown(t.Context())
	})
	var output bytes.Buffer
	log := slog.New(LogHandler(slog.NewJSONHandler(&output, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /entities/{id}", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("private", private))
		span.AddEvent(private)
		span.SetStatus(codes.Error, private)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	req := httptest.NewRequest("GET", "/entities/"+private+"?secret="+private, nil)
	req.Header.Set("User-Agent", private)
	req.Header.Set("Authorization", private)
	req.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	HTTPHandler(mux, log).ServeHTTP(httptest.NewRecorder(), req)
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported %d spans", len(spans))
	}
	span := spans[0]
	if span.SpanContext.TraceID().String() != "0123456789abcdef0123456789abcdef" || span.Parent.SpanID().String() != "0123456789abcdef" {
		t.Fatal("browser trace context was lost")
	}
	for _, attr := range span.Attributes {
		if strings.Contains(attr.Value.String(), private) {
			t.Fatalf("private attribute exported: %s", attr.Key)
		}
	}
	if len(span.Events) > 0 || span.Status.Description != "" {
		t.Fatal("private events or error message exported")
	}
	if strings.Contains(output.String(), private) {
		t.Fatal("private request information logged")
	}
	for _, want := range []string{`"trace_id":"0123456789abcdef0123456789abcdef"`, `"http_route":"GET /entities/{id}"`, `"http_status":503`, `"level":"ERROR"`} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("completion log missing %s", want)
		}
	}
}
