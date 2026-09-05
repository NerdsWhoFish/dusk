package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type privateExporter struct {
	sdktrace.SpanExporter
}

func (e privateExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	filtered := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		filtered[i] = privateSpan{ReadOnlySpan: span}
	}
	return e.SpanExporter.ExportSpans(ctx, filtered)
}

type privateSpan struct {
	sdktrace.ReadOnlySpan
}

func (s privateSpan) SpanContext() trace.SpanContext {
	return s.ReadOnlySpan.SpanContext().WithTraceState(trace.TraceState{})
}

func (s privateSpan) Parent() trace.SpanContext {
	return s.ReadOnlySpan.Parent().WithTraceState(trace.TraceState{})
}

// HTTP defaults include private URLs and caller-controlled headers.
func (s privateSpan) Attributes() []attribute.KeyValue {
	var safe []attribute.KeyValue
	for _, attr := range s.ReadOnlySpan.Attributes() {
		switch attr.Key {
		case "http.route", "http.request.method", "http.method", "http.response.status_code", "http.status_code",
			"http.request.body.size", "http.response.body.size", "network.protocol.version":
			safe = append(safe, attr)
		}
	}
	return safe
}

func (s privateSpan) Status() sdktrace.Status {
	return sdktrace.Status{Code: s.ReadOnlySpan.Status().Code}
}

func (s privateSpan) Events() []sdktrace.Event { return nil }

func (s privateSpan) Links() []sdktrace.Link { return nil }
