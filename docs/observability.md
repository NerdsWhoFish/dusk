# Observability

Dusk uses the stable OpenTelemetry Go SDK to emit distributed traces over OTLP/HTTP.
It does not know which backend stores them and never needs that backend's credential when a collector runs beside it.

Telemetry is disabled when neither `OTEL_EXPORTER_OTLP_ENDPOINT` nor `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set.
An unset endpoint keeps local development and ordinary installs free of failed export attempts.

## Collector configuration

Point Dusk at an OpenTelemetry collector that accepts OTLP over HTTP:

```text
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=production
```

The collector should add backend authentication and forward the spans to their final destination.
Putting a managed backend's credential directly in Dusk works with standard OTLP headers, but it spreads that credential into the application and is not the recommended deployment.

Set `OTEL_SDK_DISABLED=true` to disable telemetry explicitly even when an endpoint is present.

## What is traced

The outer HTTP handler records inbound requests and extracts W3C Trace Context and Baggage.
Dusk's GitHub and plugin marketplace HTTP clients record outbound calls and inject the same context, so a request can be followed across that boundary.

Context-aware structured log records include `trace_id` and `span_id`.
Ordinary records without a span remain unchanged.

Internal reconcile, index, and plugin phases do not yet create manual spans.
HTTP traces therefore show the service boundary and upstream latency, not every internal operation.

## Lifecycle

The exporter batches spans in process and gets five seconds to flush during graceful shutdown.
An invalid exporter configuration fails startup rather than running a service whose telemetry was silently discarded.

The implementation and trade-offs are recorded in [ADR-0080](../adr/0080-runtime-telemetry-uses-explicit-opentelemetry-sdks.md).
