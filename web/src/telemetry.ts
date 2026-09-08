import type { EventEvent, ExceptionEvent, MeasurementEvent, TraceEvent, TransportItem } from "@grafana/faro-web-sdk";
import { createErrorReporter } from "./error-reporting.ts";

const reporter = createErrorReporter();
export const captureError = reporter.capture;

const routes = new Set([
  "/", "/search", "/graph", "/notes", "/context", "/plugins", "/events",
  "/status", "/integrity", "/drift", "/settings", "/diff",
  "/api/search", "/api/graph", "/api/notes", "/api/context", "/api/plugins",
  "/api/events", "/api/status", "/api/overview", "/api/integrity", "/api/drift",
  "/api/home", "/api/kinds", "/api/viewer", "/api/ai", "/api/ai/ask",
  "/api/entities", "/api/repository", "/api/diff", "/telemetry/config",
]);
const variableRoutes = ["/api/entities/", "/api/notes/", "/api/plugins/", "/entities/", "/entity/", "/notes/", "/plugins/"];
const methods = new Set(["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]);
const vitals = new Set(["cls", "fcp", "fid", "inp", "lcp", "ttfb"]);
const lifecycle = new Set(["session_start", "session_resume", "session_extend", "page_load"]);
const errors = new Set(["Error", "TypeError", "RangeError", "ReferenceError", "SyntaxError", "URIError", "EvalError"]);

export function telemetryRoute(raw: string): string {
  try {
    const path = new URL(raw, "https://dusk.invalid").pathname;
    if (routes.has(path)) return path;
    const prefix = variableRoutes.find((route) => path.startsWith(route));
    return prefix ? `${prefix}{id}` : "/{other}";
  } catch {
    return "/{other}";
  }
}

export function telemetryScript(raw: string): string {
  if (typeof document === "undefined") return "/assets/{bundle}";
  try {
    const url = new URL(raw, window.location.origin);
    const scripts = Array.from(document.querySelectorAll('script[type="module"][src], link[rel="modulepreload"][href]'));
    if (url.origin === window.location.origin && url.pathname.startsWith("/assets/") &&
      scripts.some(script => {
        const source = new URL(script.getAttribute("src") ?? script.getAttribute("href") ?? "", window.location.origin);
        return source.origin === url.origin && source.pathname === url.pathname;
      })) return url.pathname;
  } catch { /* Unknown frames have no public bundle identity. */ }
  return "/assets/{bundle}";
}

// Rebuild payloads from allowed fields so SDK upgrades cannot add private data.
export function redactTelemetry(item: TransportItem): TransportItem | null {
  const meta = {
    app: { name: item.meta.app?.name, environment: item.meta.app?.environment, version: item.meta.app?.version },
    sdk: { name: item.meta.sdk?.name, version: item.meta.sdk?.version },
    session: {
      id: item.meta.session?.id,
      // Faro's later sampling hook needs this flag and removes it before export.
      attributes: { isSampled: item.meta.session?.attributes?.isSampled === "true" ? "true" : "false" },
      overrides: { geoLocationTrackingEnabled: false },
    },
    page: { url: telemetryRoute(item.meta.page?.url ?? "/") },
  };
  switch (item.type) {
    case "measurement": {
      const p = item.payload as MeasurementEvent;
      if (p.type !== "web-vitals") return null;
      const values = Object.fromEntries(Object.entries(p.values).filter(([key, value]) => vitals.has(key.toLowerCase()) && Number.isFinite(value)));
      return { type: item.type, meta, payload: { type: p.type, timestamp: p.timestamp, values } };
    }
    case "event": {
      const p = item.payload as EventEvent;
      if (!lifecycle.has(p.name)) return null;
      return { type: item.type, meta, payload: { name: p.name, timestamp: p.timestamp } };
    }
    case "exception": {
      const p = item.payload as ExceptionEvent;
      const type = errors.has(p.type) ? p.type : "Error";
      return {
        type: item.type, meta,
        payload: { type, value: type, timestamp: p.timestamp,
          trace: p.trace && /^[0-9a-f]{32}$/i.test(p.trace.trace_id) && /^[0-9a-f]{16}$/i.test(p.trace.span_id)
            ? { trace_id: p.trace.trace_id, span_id: p.trace.span_id } : undefined,
          stacktrace: {
          frames: (p.stacktrace?.frames ?? []).map((frame) => ({
            filename: telemetryScript(frame.filename), function: "{function}", lineno: frame.lineno, colno: frame.colno,
          })),
        } },
      };
    }
    case "trace": {
      const p = item.payload as TraceEvent;
      return {
        type: item.type, meta,
        payload: {
          resourceSpans: p.resourceSpans?.map((resource) => ({
            resource: { attributes: [
              { key: "service.name", value: { stringValue: "dusk-web" } },
              { key: "service.version", value: { stringValue: meta.app.version ?? "unknown" } },
            ], droppedAttributesCount: 0 },
            scopeSpans: resource.scopeSpans.map((scope) => ({
              scope: { name: "dusk.browser" },
              spans: scope.spans?.map((span) => {
                const url = span.attributes.find((attr) => ["http.url", "url.full"].includes(attr.key))?.value?.stringValue ?? "/";
                const method = span.attributes.find((attr) => ["http.method", "http.request.method"].includes(attr.key))?.value?.stringValue ?? "GET";
                const safeMethod = methods.has(method) ? method : "OTHER";
                return {
                  traceId: span.traceId, spanId: span.spanId, parentSpanId: span.parentSpanId,
                  name: `${safeMethod} ${telemetryRoute(url)}`, kind: span.kind,
                  startTimeUnixNano: span.startTimeUnixNano, endTimeUnixNano: span.endTimeUnixNano,
                  attributes: [
                    { key: "http.route", value: { stringValue: telemetryRoute(url) } },
                    { key: "http.request.method", value: { stringValue: safeMethod } },
                    ...span.attributes.filter((attr) => ["http.status_code", "http.response.status_code"].includes(attr.key) && attr.value?.intValue != null),
                  ],
                  status: { code: span.status.code }, events: [], links: [],
                  droppedAttributesCount: 0, droppedEventsCount: 0, droppedLinksCount: 0,
                };
              }),
            })),
          })),
        },
      };
    }
    default:
      return null;
  }
}

export async function initializeTelemetry(): Promise<void> {
  const onError = (event: ErrorEvent) => captureError(event.error ?? new Error("Browser error"));
  const onRejection = (event: PromiseRejectionEvent) => captureError(event.reason);
  window.addEventListener("error", onError);
  window.addEventListener("unhandledrejection", onRejection);
  try {
    const response = await fetch("/telemetry/config", { signal: AbortSignal.timeout(3000) });
    if (!response.ok) return;
    const config: { url?: string; environment?: string; version?: string } = await response.json();
    if (!config.url) return;
    const [{ initializeFaro, SessionInstrumentation, ErrorsInstrumentation, WebVitalsInstrumentation }, { TracingInstrumentation }] = await Promise.all([
      import("@grafana/faro-web-sdk"), import("@grafana/faro-web-tracing"),
    ]);
    const faro = initializeFaro({
      url: config.url,
      app: { name: "dusk-web", environment: config.environment, version: config.version },
      metas: [() => ({ page: { url: telemetryRoute(window.location.href) } })],
      beforeSend: redactTelemetry,
      sessionTracking: { persistent: false },
      instrumentations: [
        new SessionInstrumentation(), new ErrorsInstrumentation(), new WebVitalsInstrumentation(),
        new TracingInstrumentation({ instrumentationOptions: { propagateTraceHeaderCorsUrls: [window.location.origin] } }),
      ],
    });
    reporter.connect((error) => faro.api.pushError(error));
    window.removeEventListener("error", onError);
    window.removeEventListener("unhandledrejection", onRejection);
    faro.api.pushEvent("page_load");
  } catch {
    // A collector outage must not prevent the catalog from loading.
  }
}
