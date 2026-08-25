# 80. Runtime telemetry uses explicit OpenTelemetry SDKs

Date: 2026-08-25

## Status

Accepted.

## Context and Problem Statement

Dusk emits structured logs but has no distributed traces, so an HTTP request cannot be followed through GitHub calls or correlated with the log records it caused.
The deployment should export through any OpenTelemetry collector without coupling the application to one observability vendor or putting a vendor credential in the process.
The instrumentation also has to preserve the pure-Go build, distroless image, and arm64 support protected by [ADR-0017](0017-engineering-policy.md).

## Considered Options

1. Use the stable OpenTelemetry Go SDK explicitly at the HTTP server and client boundaries, with standard OTLP environment variables.
2. Use compile-time automatic instrumentation.
3. Use privileged eBPF automatic instrumentation in the cluster.
4. Keep structured logs only and infer request flow from identifiers added by hand.

## Decision Outcome

Chosen: **option 1**.
Dusk owns a small internal telemetry package that configures the upstream OpenTelemetry SDK, exports OTLP over HTTP, propagates W3C trace context, instruments inbound and outbound HTTP, and adds trace identifiers to context-aware structured logs.

The collector owns authentication and vendor routing.
Dusk receives only a local OTLP endpoint through the standard `OTEL_EXPORTER_OTLP_*` environment variables, and telemetry remains disabled when no endpoint is configured.

The package is internal because it owns Dusk's process-wide globals and lifecycle.
Another repository using OpenTelemetry should use the upstream SDK directly rather than importing Dusk and inheriting its service-specific policy.

### Good

- The wire protocol and configuration are vendor neutral, while the deployment can route to Grafana Cloud, a local collector, or another backend without a code change.
- The application never receives the Grafana Cloud credential.
- Explicit boundaries produce stable span names and avoid requiring privileged access to the host.
- Trace and span identifiers make a structured log directly reachable from a trace.
- An unset endpoint preserves the current zero-configuration behavior.

### Bad

- Every boundary worth tracing still has to be instrumented deliberately, so an unwrapped client or protocol is invisible.
- The process owns a global tracer provider and propagator, which makes repeated initialization inappropriate.
- OpenTelemetry adds several dependencies and a background batch exporter to a service that previously needed neither.
- HTTP spans do not explain internal reconcile, index, or plugin phases until those operations gain manual spans.

### Rejected because

- **Option 2** is newer and less explicit, and a compiler transformation is a poor place to hide a production service's runtime behavior while the stable SDK already covers its important boundaries.
- **Option 3** requires privileged host access and couples observability to a particular deployment shape, breaking the ordinary container installation Dusk supports.
- **Option 4** does not preserve parent context across services and makes latency attribution guesswork, which is the problem tracing is meant to solve.
