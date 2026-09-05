import type { EventEvent, ExceptionEvent, MeasurementEvent, TraceEvent, TransportItem } from "@grafana/faro-web-sdk";

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

// Rebuild payloads from allowed fields so SDK upgrades cannot add private data.
export function redactTelemetry(item: TransportItem): TransportItem | null {
  const meta = {
    app: { name: item.meta.app?.name, environment: item.meta.app?.environment, version: item.meta.app?.version },
    sdk: { name: item.meta.sdk?.name, version: item.meta.sdk?.version },
    session: { id: item.meta.session?.id, overrides: { geoLocationTrackingEnabled: false } },
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
        payload: { type, value: type, timestamp: p.timestamp, stacktrace: {
          frames: (p.stacktrace?.frames ?? []).map((frame) => ({
            filename: "/assets/{bundle}", function: "{function}", lineno: frame.lineno, colno: frame.colno,
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
            resource: { attributes: [{ key: "service.name", value: { stringValue: "dusk-web" } }], droppedAttributesCount: 0 },
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
  try {
    const response = await fetch("/telemetry/config", { signal: AbortSignal.timeout(3000) });
    if (!response.ok) return;
    const config: { url?: string; environment?: string } = await response.json();
    if (!config.url) return;
    const [{ initializeFaro, SessionInstrumentation, ErrorsInstrumentation, WebVitalsInstrumentation }, { TracingInstrumentation }] = await Promise.all([
      import("@grafana/faro-web-sdk"), import("@grafana/faro-web-tracing"),
    ]);
    const faro = initializeFaro({
      url: config.url,
      app: { name: "dusk-web", environment: config.environment },
      metas: [() => ({ page: { url: telemetryRoute(window.location.href) } })],
      beforeSend: redactTelemetry,
      sessionTracking: { persistent: false },
      instrumentations: [
        new SessionInstrumentation(), new ErrorsInstrumentation(), new WebVitalsInstrumentation(),
        new TracingInstrumentation({ instrumentationOptions: { propagateTraceHeaderCorsUrls: [window.location.origin] } }),
      ],
    });
    faro.api.pushEvent("page_load");
  } catch {
    // A collector outage must not prevent the catalog from loading.
  }
}
