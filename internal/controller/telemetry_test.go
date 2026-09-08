package controller

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestReconcileFailureHasCorrelatedPrivateTelemetry(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	var logs bytes.Buffer
	c := &Controller{opts: Options{Logger: slog.New(telemetry.LogHandler(slog.NewJSONHandler(&logs, nil)))}}
	if err := c.reconcileAt(context.Background(), nil, "private-invalid-input", "", ""); err == nil {
		t.Fatal("invalid repository accepted")
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "dusk.controller.reconcile" || spans[0].Status().Code != codes.Error {
		t.Fatalf("missing failed reconcile span: %v", spans)
	}
	span := spans[0]
	if span.Status().Description != "" || len(span.Events()) != 0 || len(span.Attributes()) != 0 {
		t.Fatal("reconcile span contains private error detail")
	}
	for _, want := range []string{"reconcile failed", "error_type", span.SpanContext().TraceID().String(), span.SpanContext().SpanID().String()} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q: %s", want, logs.String())
		}
	}
	if strings.Contains(logs.String(), "private-invalid-input") {
		t.Fatal("log contains raw error input")
	}
}
