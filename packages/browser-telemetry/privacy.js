const methods = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']);
const errors = new Set(['Error', 'TypeError', 'RangeError', 'ReferenceError', 'SyntaxError', 'URIError', 'EvalError', 'UnhandledRejection']);
const vitals = new Set(['cls', 'fcp', 'fid', 'inp', 'lcp', 'ttfb']);
const lifecycle = new Set(['session_start', 'session_resume', 'session_extend', 'page_load']);
const traceID = /^[0-9a-f]{32}$/i;
const spanID = /^[0-9a-f]{16}$/i;

export function privacyFilter({ app, origin, routes = [], assets = [], operations = [] }) {
  const allowedRoutes = new Set(routes);
  const allowedAssets = new Set(assets);
  const allowedOperations = new Set(operations);
  const route = (raw) => {
    try {
      const url = new URL(raw, origin);
      return url.origin === origin && allowedRoutes.has(url.pathname) ? url.pathname : '/{other}';
    } catch { return '/{other}'; }
  };
  const stackFile = (raw) => {
    try {
      const url = new URL(raw, origin);
      return url.origin === origin && allowedAssets.has(url.pathname) ? url.pathname : '/{script}';
    } catch { return '/{script}'; }
  };
  const trace = (value) => value && traceID.test(value.trace_id) && spanID.test(value.span_id)
    ? { trace_id: value.trace_id, span_id: value.span_id } : undefined;
  return (item) => {
    const meta = {
      app: { name: app.name, version: app.version, environment: app.environment },
      sdk: { name: item.meta.sdk?.name, version: item.meta.sdk?.version },
      session: {
        id: item.meta.session?.id,
        // Faro consumes this control attribute after beforeSend, then removes it.
        attributes: { isSampled: item.meta.session?.attributes?.isSampled === 'true' ? 'true' : 'false' },
        overrides: { geoLocationTrackingEnabled: false },
      },
      page: { url: route(item.meta.page?.url) },
    };
    const p = item.payload;
    if (item.type === 'exception') {
      const type = errors.has(p.type) ? p.type : 'Error';
      const operation = allowedOperations.has(p.context?.operation) ? p.context.operation : undefined;
      return { type: item.type, meta, payload: {
        type, value: type, timestamp: p.timestamp, fatal: p.fatal, trace: trace(p.trace),
        context: operation ? { operation } : undefined,
        stacktrace: { frames: (p.stacktrace?.frames ?? []).map(frame => ({
          filename: stackFile(frame.filename), function: '{function}', lineno: frame.lineno, colno: frame.colno,
        })) },
      } };
    }
    if (item.type === 'event' && lifecycle.has(p.name)) {
      return { type: item.type, meta, payload: { name: p.name, timestamp: p.timestamp } };
    }
    if (item.type === 'measurement' && p.type === 'web-vitals') {
      const values = Object.fromEntries(Object.entries(p.values).filter(([key, value]) => vitals.has(key.toLowerCase()) && Number.isFinite(value)));
      return { type: item.type, meta, payload: { type: p.type, timestamp: p.timestamp, values } };
    }
    if (item.type !== 'trace') return null;
    return { type: item.type, meta, payload: {
      resourceSpans: p.resourceSpans?.map(resource => ({
        resource: { attributes: [
          { key: 'service.name', value: { stringValue: app.name } },
          { key: 'service.version', value: { stringValue: app.version } },
          { key: 'deployment.environment.name', value: { stringValue: app.environment } },
        ], droppedAttributesCount: 0 },
        scopeSpans: resource.scopeSpans.map(scope => ({
          scope: { name: 'browser.http' },
          spans: scope.spans?.map(span => {
            const url = span.attributes.find(a => ['http.url', 'url.full'].includes(a.key))?.value?.stringValue;
            const method = span.attributes.find(a => ['http.method', 'http.request.method'].includes(a.key))?.value?.stringValue;
            const safeMethod = methods.has(method) ? method : 'OTHER';
            return {
              traceId: span.traceId, spanId: span.spanId, parentSpanId: span.parentSpanId,
              name: `${safeMethod} ${route(url)}`, kind: span.kind,
              startTimeUnixNano: span.startTimeUnixNano, endTimeUnixNano: span.endTimeUnixNano,
              attributes: [
                { key: 'http.route', value: { stringValue: route(url) } },
                { key: 'http.request.method', value: { stringValue: safeMethod } },
                ...span.attributes.filter(a => ['http.status_code', 'http.response.status_code'].includes(a.key) && a.value?.intValue != null),
              ],
              status: { code: span.status.code }, events: [], links: [],
              droppedAttributesCount: 0, droppedEventsCount: 0, droppedLinksCount: 0,
            };
          }),
        })),
      })),
    } };
  };
}
