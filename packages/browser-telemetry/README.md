# Browser telemetry

Privacy-filtered Grafana Faro capture for browser applications, with no Dusk runtime dependency.

Install the versioned tarball attached to a Dusk release and commit the package lock's integrity hash. Bundle it into the application's own assets; do not load code from another application at runtime. The official Faro SDK handles uncaught exceptions, rejected promises, tracing, batching and transport. This package supplies the shared privacy policy and a handled-error API.

```js
import { initializeTelemetry } from '@nerdswhofish/browser-telemetry';

const telemetry = initializeTelemetry({
  url: publicCollectorURL,
  app: { name: 'storefront', version: buildCommit, environment: 'production' },
  routes: ['/', '/api/checkout'],
  assets: ['/assets/app.js'],
  operations: ['checkout'],
});
try {
  await checkout();
} catch (error) {
  telemetry.captureError(error, 'checkout');
}
```

Initialize synchronously before application code. A parser-blocking first-party bundle in the document head also observes failures in later scripts. React applications should supply captureError to their root error callbacks. Caught exceptions require explicit reporting; cancellations are ignored and repeated reports of the same Error object are deduplicated.

Routes, script paths and operation names are explicit allowlists. Payloads omit user data, arbitrary URLs, queries, fragments, cookies, headers, bodies, console messages and exception messages. Only configured public script paths and numeric stack locations survive for release-specific diagnosis. Sessions are tab-scoped, sampled at 100%, with geographic enrichment disabled. Replay and console capture are not installed. Each application must supply its actual build revision, never a fixed placeholder version.

Localhost telemetry is disabled unless local=true, and even then a remote collector is rejected. Tests can inject a capture transport. Browser delivery still depends on the browser and network; this package does not promise delivery after a crash or closed tab.

Run npm ci and npm test here. npm pack produces the independently installable release artifact. See the repository ADR for why the package shares this repository and how consumers remain independent of Dusk releases at runtime.
