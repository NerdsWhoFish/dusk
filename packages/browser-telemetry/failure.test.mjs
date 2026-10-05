import assert from 'node:assert/strict';
import { test } from 'node:test';
import { installDOM } from './test-dom.mjs';

test('a rejected Web Vitals listener preserves tracing, lifecycle and error capture', async () => {
  const dom = installDOM();
  const sdk = await import('@grafana/faro-web-sdk');
  const { TracingInstrumentation } = await import('@grafana/faro-web-tracing');
  const { initializeTelemetry } = await import('./index.js');
  const initializeTracing = TracingInstrumentation.prototype.initialize;
  let tracingInitialized = false;
  TracingInstrumentation.prototype.initialize = function () {
    initializeTracing.call(this);
    tracingInitialized = true;
  };
  const addEventListener = globalThis.addEventListener;
  let rejectedListeners = 0;
  globalThis.addEventListener = (type, ...args) => {
    if (type === 'visibilitychange') {
      rejectedListeners++;
      throw new TypeError('Can only call EventTarget.addEventListener on instances of EventTarget');
    }
    return addEventListener(type, ...args);
  };
  const items = [];
  class CaptureTransport extends sdk.BaseTransport {
    name = 'capture';
    version = '1';
    initialize() {}
    send(batch) { items.push(...JSON.parse(JSON.stringify(Array.isArray(batch) ? batch : [batch]))); }
  }
  let telemetry;
  try {
    telemetry = initializeTelemetry({
      app: { name: 'test-site', version: 'abc123', environment: 'test' },
      routes: ['/api/checkout'], operations: ['checkout'], local: true,
      transports: [new CaptureTransport()],
    });
    assert.equal(rejectedListeners, 1);
    assert.equal(tracingInitialized, true);
    const error = new Error('private handled error');
    telemetry.captureError(error, 'checkout');
    telemetry.captureError(error, 'checkout');
    const event = new dom.window.Event('unhandledrejection');
    Object.defineProperty(event, 'reason', { value: new Error('private rejection') });
    dom.window.dispatchEvent(event);
    await new Promise(resolve => setTimeout(resolve, 300));
    const exceptions = items.filter(item => item.type === 'exception');
    assert.equal(exceptions.length, 3);
    assert.equal(exceptions[0].payload.type, 'TypeError');
    assert.equal(exceptions[0].payload.context.operation, 'telemetry.web_vitals.initialize');
    assert.equal(exceptions[0].payload.context.error_reason, 'invalid_receiver');
    assert.equal(exceptions[0].payload.value, 'TypeError');
    assert.equal(exceptions[1].payload.context.operation, 'checkout');
    assert.ok(items.some(item => item.type === 'event' && item.payload.name === 'page_load'));
    assert.ok(!globalThis.faro.instrumentations.instrumentations.some(item => item instanceof sdk.WebVitalsInstrumentation));
    assert.ok(!JSON.stringify(items).includes('private'));
    assert.ok(!JSON.stringify(items).includes('customer@example.test'));
    assert.ok(!JSON.stringify(items).includes('Can only call'));
  } finally {
    TracingInstrumentation.prototype.initialize = initializeTracing;
    globalThis.addEventListener = addEventListener;
    telemetry?.dispose();
    globalThis.faro?.pause();
    await dom.restore();
  }
});
