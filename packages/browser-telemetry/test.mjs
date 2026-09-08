import assert from 'node:assert/strict';
import { test } from 'node:test';
import { privacyFilter } from './privacy.js';
import { JSDOM } from 'jsdom';

test('every signal is rebuilt from safe fields while retaining useful failure identity', () => {
  const privateValue = 'customer@example.test';
  const filter = privacyFilter({
    app: { name: 'site', version: 'abc123', environment: 'production' },
    origin: 'https://site.test', routes: ['/api/checkout'], assets: ['/assets/app.js'], operations: ['checkout'],
  });
  const meta = {
    page: { url: `https://site.test/api/checkout?email=${privateValue}#private` },
    user: { email: privateValue }, session: { id: 'random', attributes: { isSampled: 'true', secret: privateValue } },
  };
  const exception = filter({ type: 'exception', meta, payload: {
    type: 'TypeError', value: privateValue, context: { operation: 'checkout', secret: privateValue },
    stacktrace: { frames: [{ filename: `https://site.test/assets/app.js?secret=${privateValue}`, function: privateValue, lineno: 3, colno: 7 }] },
  } });
  assert.equal(exception.payload.context.operation, 'checkout');
  assert.equal(exception.payload.stacktrace.frames[0].filename, '/assets/app.js');
  assert.equal(exception.meta.app.version, 'abc123');
  assert.equal(exception.meta.page.url, '/api/checkout');
  assert.ok(!JSON.stringify(exception).includes(privateValue));
  assert.equal(filter({ type: 'log', meta, payload: { message: privateValue } }), null);
  assert.equal(filter({ type: 'event', meta, payload: { name: privateValue } }), null);
  const measurement = filter({ type: 'measurement', meta, payload: { type: 'web-vitals', values: { lcp: 20, [privateValue]: 1 } } });
  assert.deepEqual(measurement.payload.values, { lcp: 20 });
  const event = filter({ type: 'event', meta, payload: { name: 'page_load', attributes: { secret: privateValue } } });
  assert.ok(!JSON.stringify(event).includes(privateValue));
});

test('real Faro transport receives sanitized handled errors and unhandled rejections', async () => {
  const dom = new JSDOM('<html><body></body></html>', { url: 'http://localhost/api/checkout?secret=private' });
  const originals = new Map();
  for (const key of ['window', 'document', 'navigator', 'location', 'sessionStorage', 'localStorage', 'XMLHttpRequest', 'Event', 'ErrorEvent', 'addEventListener', 'removeEventListener']) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value: key.endsWith('EventListener') ? dom.window[key].bind(dom.window) : dom.window[key] });
  }
  const items = [];
  const sdk = await import('@grafana/faro-web-sdk');
  class CaptureTransport extends sdk.BaseTransport {
    name = 'capture';
    version = '1';
    initialize() {}
    send(batch) { items.push(...JSON.parse(JSON.stringify(Array.isArray(batch) ? batch : [batch]))); }
  }
  let telemetry;
  try {
    const { initializeTelemetry } = await import('./index.js');
    telemetry = initializeTelemetry({
      app: { name: 'test-site', version: 'abc123', environment: 'test' },
      routes: ['/api/checkout'], operations: ['checkout'], local: true,
      transports: [new CaptureTransport()],
    });
    const error = new TypeError('customer@example.test');
    telemetry.captureError(error, 'checkout');
    telemetry.captureError(error, 'checkout');
    const event = new dom.window.Event('unhandledrejection');
    Object.defineProperty(event, 'reason', { value: new Error('private-rejection') });
    dom.window.dispatchEvent(event);
    await new Promise(resolve => setTimeout(resolve, 300));
    const exceptions = items.filter(item => item.type === 'exception');
    assert.equal(exceptions.length, 2);
    assert.equal(exceptions[0].payload.context.operation, 'checkout');
    const wire = JSON.stringify(items);
    assert.ok(!wire.includes('customer@example.test'));
    assert.ok(!wire.includes('private-rejection'));
    assert.ok(!wire.includes('?secret'));
    assert.ok(wire.includes('abc123'));
  } finally {
    telemetry?.dispose();
    // OTel's scheduled callback still needs the DOM after disposal.
    await new Promise(resolve => setTimeout(resolve, 1100));
    dom.window.close();
    for (const [key, descriptor] of originals) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else delete globalThis[key];
    }
  }
});
