package telemetry

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

func TestHandledForbiddenIsNotAnErrorSpan(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		handled bool
		want    codes.Code
	}{
		{name: "an ordinary forbidden response is an error span", status: http.StatusForbidden, want: codes.Error},
		{name: "a handled forbidden response keeps the status code and is not an error span", status: http.StatusForbidden, handled: true, want: codes.Ok},
		{name: "a handled server error stays an error span", status: http.StatusInternalServerError, handled: true, want: codes.Error},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				otel.SetTracerProvider(previous)
				_ = provider.Shutdown(context.Background())
			})

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, "no")
			}))
			t.Cleanup(server.Close)

			ctx := t.Context()
			if tt.handled {
				ctx = HandledForbidden(ctx)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/comments", strings.NewReader("body"))
			if err != nil {
				t.Fatal(err)
			}
			res, err := HTTPClient(0).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, res.Body)
			if err := res.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.status)
			}

			spans := recorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("spans = %d, want 1", len(spans))
			}
			if got := spans[0].Status().Code; got != tt.want {
				t.Fatalf("span status = %s, want %s", got, tt.want)
			}
			if got := attributeInt(t, spans[0], "http.response.status_code"); got != tt.status {
				t.Fatalf("http.response.status_code = %d, want %d", got, tt.status)
			}
		})
	}
}

func attributeInt(t *testing.T, span sdktrace.ReadOnlySpan, key string) int {
	t.Helper()
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return int(attr.Value.AsInt64())
		}
	}
	t.Fatalf("span missing %s: %v", key, span.Attributes())
	return 0
}
