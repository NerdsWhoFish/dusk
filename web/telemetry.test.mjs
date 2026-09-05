import assert from "node:assert/strict";
import { test } from "node:test";
import { redactTelemetry, telemetryRoute } from "./src/telemetry.ts";

const privateValue = "customer@example.com";
const meta = {
  app: { name: "dusk-web", installationId: privateValue },
  sdk: { name: "faro-web-sdk", integrations: [{ name: privateValue }] },
  page: { url: `https://dusk.example/api/entities/${privateValue}?secret=${privateValue}` },
  user: { email: privateValue },
  browser: { userAgent: privateValue },
  session: { id: "random-session", attributes: { name: privateValue, isSampled: "true" } },
};

test("route templates discard query strings, fragments and arbitrary identifiers", () => {
  assert.equal(telemetryRoute(`/api/search?q=${privateValue}#secret`), "/api/search");
  assert.equal(telemetryRoute(`/api/entities/${privateValue}/dependents`), "/api/entities/{id}");
  assert.equal(telemetryRoute(`/${privateValue}`), "/{other}");
});

test("all browser signal types strip private data before transport", () => {
  const timestamp = new Date().toISOString();
  const items = [
    { type: "measurement", payload: { type: "web-vitals", values: { lcp: 42, [privateValue]: 7 }, timestamp, context: { selector: privateValue }, action: { name: privateValue } } },
    { type: "event", payload: { name: "page_load", timestamp, attributes: { url: privateValue } } },
    { type: "exception", payload: { type: "TypeError", value: privateValue, timestamp, context: { input: privateValue }, stacktrace: { frames: [{ filename: privateValue, function: privateValue, lineno: 3 }] } } },
    { type: "trace", payload: { resourceSpans: [{ resource: { attributes: [{ key: "user", value: { stringValue: privateValue } }] }, scopeSpans: [{ scope: { name: privateValue }, spans: [{
      traceId: "0123456789abcdef0123456789abcdef", spanId: "0123456789abcdef", parentSpanId: "fedcba9876543210",
      name: privateValue, traceState: privateValue, kind: 3, startTimeUnixNano: "1000", endTimeUnixNano: "2000",
      attributes: [{ key: "url.full", value: { stringValue: `https://dusk.example/api/search?q=${privateValue}` } }, { key: "http.request.header.authorization", value: { stringValue: privateValue } }, { key: "http.response.status_code", value: { intValue: 200 } }],
      status: { code: 2, message: privateValue }, events: [{ name: privateValue }], links: [{ traceState: privateValue }],
    }] }] }] } },
  ];
  for (const item of items) {
    const result = redactTelemetry({ ...item, meta });
    assert.ok(result);
    assert.ok(!JSON.stringify(result).includes(privateValue), item.type);
    assert.equal(result.meta.page.url, "/api/entities/{id}");
    assert.equal(result.meta.session.overrides.geoLocationTrackingEnabled, false);
    assert.deepEqual(result.meta.session.attributes, { isSampled: "true" });
  }
  const span = redactTelemetry({ ...items[3], meta }).payload.resourceSpans[0].scopeSpans[0].spans[0];
  assert.equal(span.name, "GET /api/search");
  assert.equal(span.traceId, "0123456789abcdef0123456789abcdef");
  assert.equal(span.parentSpanId, "fedcba9876543210");
  assert.equal(span.attributes.at(-1).value.intValue, 200);
});

test("unrecognized signals and console messages are rejected", () => {
  for (const item of [
    { type: "log", payload: { message: privateValue } },
    { type: "event", payload: { name: privateValue } },
    { type: "measurement", payload: { type: privateValue } },
  ]) assert.equal(redactTelemetry({ ...item, meta }), null);
});
