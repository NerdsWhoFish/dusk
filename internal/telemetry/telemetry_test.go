package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestStartWithoutEndpointLeavesProviderAlone(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	before := otel.GetTracerProvider()
	shutdown, err := Start(t.Context(), "dusk", "test", slog.Default())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := shutdown(t.Context()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := otel.GetTracerProvider(); got != before {
		t.Fatal("Start changed the global provider without an OTLP endpoint")
	}
}

func TestLogHandlerAddsTraceContext(t *testing.T) {
	var output bytes.Buffer
	log := slog.New(LogHandler(slog.NewJSONHandler(&output, nil)))
	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})

	log.InfoContext(trace.ContextWithSpanContext(context.Background(), span), "correlated")

	for _, want := range []string{`"trace_id":"01000000000000000000000000000000"`, `"span_id":"0200000000000000"`} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("log output %q does not contain %q", output.String(), want)
		}
	}
}
