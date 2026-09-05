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
Completed requests log their route template, status, and duration; health probes are omitted.
Server exports keep HTTP methods, route templates, statuses, body sizes, and protocol versions.
Raw URLs, query strings, headers, network addresses, span events, and error descriptions are discarded before export.

## Browser telemetry

Set `DUSK_FARO_URL` to an HTTPS Faro collector URL to enable browser RUM.
`DUSK_ENVIRONMENT` identifies the deployment and defaults to `production`.
The UI reads these public settings from `/telemetry/config` at runtime, so the same image works across deployments.
An unset collector disables RUM; a failed configuration request never blocks the UI.

Restrict the collector to the deployment's browser origin.
Use only Faro's public collector identifier, never an OTLP ingestion or Grafana API credential.
The browser SDK records Web Vitals, sanitized browser errors, session lifecycle events, and fetch/XHR traces.
W3C context links same-origin API calls to server traces and their completion logs.

Every browser payload passes through an allowlist before transport.
URLs become route templates, error messages and function names are removed, and span attributes retain only methods, route templates, and status codes.
Console logs, DOM interactions, form values, request bodies, user identity, session replay, and geolocation are not collected.
Session IDs are random and use tab-scoped session storage, so reloads remain correlated without persistent local storage or cookies.

Run `npm run check` in `web/` for the payload privacy regression checks.
After deployment, exercise an API call in the browser, confirm Faro accepts its payload, and find its trace ID in both the trace backend and request completion logs.

## Internal tracing

Internal reconcile, index, and plugin phases do not yet create manual spans.
HTTP traces therefore show the service boundary and upstream latency, not every internal operation.

## Lifecycle

The exporter batches spans in process and gets five seconds to flush during graceful shutdown.
An invalid exporter configuration fails startup rather than running a service whose telemetry was silently discarded.

The implementation and trade-offs are recorded in [ADR-0080](../adr/0080-runtime-telemetry-uses-explicit-opentelemetry-sdks.md).
