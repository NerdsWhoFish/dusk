import { initializeFaro, SessionInstrumentation, ErrorsInstrumentation, WebVitalsInstrumentation } from '@grafana/faro-web-sdk';
import { TracingInstrumentation } from '@grafana/faro-web-tracing';
import { privacyFilter } from './privacy.js';

export { privacyFilter } from './privacy.js';

export function initializeTelemetry({ url, app, routes, assets, operations, local = false, transports }) {
  const loopback = new Set(['localhost', '127.0.0.1', '[::1]']);
  const report = { captureError() {}, dispose() {} };
  if (typeof window === 'undefined') return report;
  if (loopback.has(window.location.hostname) && !local) return report;
  if (loopback.has(window.location.hostname) && url && !loopback.has(new URL(url).hostname)) return report;
  const seen = new WeakSet();
  const faro = initializeFaro({
    url: transports ? undefined : url,
    app,
    transports,
    batching: { enabled: true, sendTimeout: 250, itemLimit: 50 },
    beforeSend: privacyFilter({ app, origin: window.location.origin, routes, assets, operations }),
    sessionTracking: { persistent: false, samplingRate: 1 },
    instrumentations: [
      new SessionInstrumentation(), new ErrorsInstrumentation(), new WebVitalsInstrumentation(),
      new TracingInstrumentation({ instrumentationOptions: { propagateTraceHeaderCorsUrls: [window.location.origin] } }),
    ],
  });
  report.captureError = (value, operation) => {
    const error = value instanceof Error ? value : new Error('Browser error');
    if (error.name === 'AbortError' || seen.has(error)) return;
    seen.add(error);
    faro.api.pushError(error, { context: { operation } });
  };
  report.dispose = () => {
    faro.instrumentations.remove(...faro.instrumentations.instrumentations);
    faro.pause();
  };
  faro.api.pushEvent('page_load');
  return report;
}
