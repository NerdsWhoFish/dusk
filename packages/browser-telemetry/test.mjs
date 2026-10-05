import assert from 'node:assert/strict';
import { test } from 'node:test';
import { privacyFilter } from './privacy.js';
import { installDOM } from './test-dom.mjs';

test('every signal is rebuilt from safe fields while retaining useful failure identity', () => {
  const privateValue = 'customer@example.test';
  const filter = privacyFilter({
    app: { name: 'site', version: 'abc123', environment: 'production' },
    origin: 'https://site.test', routes: ['/api/checkout'], assets: ['/assets/app.js'], operations: ['checkout'],
  });
  const meta = {
    page: { url: `https://site.test/api/checkout?email=${privateValue}#private` },
    user: { email: privateValue }, session: { id: 'random', attributes: { isSampled: 'true', secret: privateValue } },
    browser: { name: 'Mobile Safari', mobile: true, version: privateValue, os: privateValue, userAgent: privateValue, language: privateValue, viewportWidth: privateValue, brands: [privateValue] },
  };
  const exception = filter({ type: 'exception', meta, payload: {
    type: 'TypeError', value: privateValue, context: { operation: 'checkout', secret: privateValue },
    stacktrace: { frames: [{ filename: `https://site.test/assets/app.js?secret=${privateValue}`, function: privateValue, lineno: 3, colno: 7 }] },
  } });
  assert.equal(exception.payload.context.operation, 'checkout');
  assert.equal(exception.payload.stacktrace.frames[0].filename, '/assets/app.js');
  assert.equal(exception.meta.app.version, 'abc123');
  assert.equal(exception.meta.page.url, '/api/checkout');
  assert.deepEqual(exception.meta.browser, { name: 'Mobile Safari', mobile: true });
  assert.ok(!JSON.stringify(exception).includes(privateValue));
  assert.equal(filter({ type: 'log', meta, payload: { message: privateValue } }), null);
  assert.equal(filter({ type: 'event', meta, payload: { name: privateValue } }), null);
  const measurement = filter({ type: 'measurement', meta, payload: { type: 'web-vitals', values: { lcp: 20, [privateValue]: 1 } } });
  assert.deepEqual(measurement.payload.values, { lcp: 20 });
  const event = filter({ type: 'event', meta, payload: { name: 'page_load', attributes: { secret: privateValue } } });
  assert.ok(!JSON.stringify(event).includes(privateValue));
});

test('error reasons are exact fixed classifications, never caller text', () => {
  const filter = privacyFilter({ app: { name: 'test' }, origin: 'https://site.test' });
  const cases = [
    ['Illegal invocation', 'invalid_receiver'],
    ["Failed to execute 'addEventListener' on 'EventTarget': Illegal invocation", 'invalid_receiver'],
    ['Can only call EventTarget.addEventListener on instances of EventTarget', 'invalid_receiver'],
    ["Failed to execute 'addEventListener' on 'EventTarget': parameter 2 is not of type 'Object'.", 'invalid_event_listener'],
    ["Argument 2 ('listener') to EventTarget.addEventListener must be an object", 'invalid_event_listener'],
    ['customer@example.test', 'other'],
    ['Illegal invocation customer@example.test', 'other'],
    ['TypeError: Illegal invocation', 'other'],
    [undefined, 'other'],
  ];
  for (const [value, reason] of cases) {
    const input = { type: 'exception', meta: {}, payload: { type: 'TypeError', value, context: { error_reason: 'customer@example.test' } } };
    const result = filter(input);
    assert.deepEqual(result.payload.context, { error_reason: reason });
    assert.equal(result.payload.value, 'TypeError');
    assert.ok(!JSON.stringify(result).includes('customer@example.test'));
    input.payload.type = 'Error';
    assert.equal(filter(input).payload.context.error_reason, 'other');
  }
});

test('browser metadata preserves only allowlisted names and boolean mobile flags', () => {
  const filter = privacyFilter({ app: { name: 'test' }, origin: 'https://site.test' });
  for (const [browser, expected] of [
    [{ name: 'WebKit', mobile: true, userAgent: 'private' }, { name: 'WebKit', mobile: true }],
    [{ name: 'Chrome', mobile: false, version: 'private' }, { name: 'Chrome', mobile: false }],
    [{ name: 'Chrome private', mobile: 'private' }, { name: 'Other' }],
    [undefined, { name: 'Other' }],
  ]) {
    const result = filter({ type: 'event', meta: { browser }, payload: { name: 'page_load' } });
    assert.deepEqual(result.meta.browser, expected);
    assert.ok(!JSON.stringify(result).includes('private'));
  }
});

test('real Faro transport receives sanitized handled errors and unhandled rejections', async () => {
  const dom = installDOM();
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
    const disabled = initializeTelemetry({ app: { name: 'disabled' }, local: true });
    assert.doesNotThrow(() => disabled.captureError(new Error('disabled')));
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
    const nativeError = new TypeError('Illegal invocation');
    dom.window.onerror(nativeError.message, 'https://third-party.test/private-script.js', 1, 2, nativeError);
    await new Promise(resolve => setTimeout(resolve, 300));
    const exceptions = items.filter(item => item.type === 'exception');
    assert.equal(exceptions.length, 3);
    assert.equal(exceptions[0].payload.context.operation, 'checkout');
    const nativeException = exceptions.find(item => item.payload.context.error_reason === 'invalid_receiver');
    assert.equal(nativeException.payload.type, 'TypeError');
    assert.equal(nativeException.payload.value, 'TypeError');
    const wire = JSON.stringify(items);
    assert.ok(!wire.includes('customer@example.test'));
    assert.ok(!wire.includes('private-rejection'));
    assert.ok(!wire.includes('?secret'));
    assert.ok(!wire.includes('Illegal invocation'));
    assert.ok(!wire.includes('third-party.test'));
    assert.ok(wire.includes('abc123'));
  } finally {
    telemetry?.dispose();
    await dom.restore();
  }
});
