package server_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/NerdsWhoFish/dusk/internal/controller"
	"github.com/NerdsWhoFish/dusk/internal/telemetry"
	"go.opentelemetry.io/otel/trace"
)

const webhookSecret = "hook-secret"

func sign(t *testing.T, body, secret string) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func post(t *testing.T, h http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The forwarder in front of Dusk deliberately does not verify signatures, so
// this handler is the only thing standing between the internet and the catalog.
func TestADR0006_WebhookRejectsAnythingUnsigned(t *testing.T) {
	const body = `{"zen":"anything"}`

	tests := []struct {
		name       string
		signature  string
		wantStatus int
	}{
		{name: "a correct signature is accepted", signature: sign(t, body, webhookSecret), wantStatus: http.StatusAccepted},
		{name: "no signature header at all is rejected", wantStatus: http.StatusUnauthorized},
		{name: "a signature from the wrong secret is rejected", signature: sign(t, body, "not-the-secret"), wantStatus: http.StatusUnauthorized},
		{name: "a malformed signature is rejected", signature: "sha256=zzzz", wantStatus: http.StatusUnauthorized},
		{name: "an unprefixed signature is rejected", signature: hex.EncodeToString([]byte("x")), wantStatus: http.StatusUnauthorized},
		{name: "an empty signature is rejected", signature: "sha256=", wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newServer(t, &fakeStore{creds: sampleCreds()}, &fakeGitHub{})

			headers := map[string]string{
				"X-GitHub-Event":    "push",
				"X-GitHub-Delivery": "delivery-" + tt.name,
			}
			if tt.signature != "" {
				headers["X-Hub-Signature-256"] = tt.signature
			}

			if rec := post(t, h, body, headers); rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestWebhookRejectsReplays(t *testing.T) {
	const body = `{"zen":"replayed"}`
	h := newServer(t, &fakeStore{creds: sampleCreds()}, &fakeGitHub{})
	headers := map[string]string{
		"X-GitHub-Event":      "push",
		"X-GitHub-Delivery":   "same-id",
		"X-Hub-Signature-256": sign(t, body, webhookSecret),
	}

	if rec := post(t, h, body, headers); rec.Code != http.StatusAccepted {
		t.Fatalf("first delivery status = %d, want 202", rec.Code)
	}

	rec := post(t, h, body, headers)
	if rec.Code != http.StatusOK {
		t.Errorf("replay status = %d, want 200 so GitHub stops retrying", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "duplicate") {
		t.Errorf("replay body = %q, want it to say duplicate", rec.Body.String())
	}
}

func TestPushDeletionDoesNotReconcile(t *testing.T) {
	for _, ref := range []string{"refs/heads/fix/catalog", "refs/tags/v1.0.0"} {
		for _, deleted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deleted=%t", ref, deleted), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					var pushes []controller.Push
					control := &fakeController{push: func(_ context.Context, push controller.Push) error {
						pushes = append(pushes, push)
						return nil
					}}
					var logs bytes.Buffer
					h := build(t, setup{store: &fakeStore{creds: sampleCreds()}, control: control,
						logger: slog.New(slog.NewJSONHandler(&logs, nil))})
					body := fmt.Sprintf(`{"ref":%q,"deleted":%t,"commits":[],"repository":{"id":1,"name":"catalog","owner":{"login":"owner"}},"installation":{"id":2}}`, ref, deleted)
					headers := map[string]string{
						"X-GitHub-Event": "push", "X-GitHub-Delivery": "push-delivery",
						"X-Hub-Signature-256": sign(t, body, webhookSecret),
					}
					if rec := post(t, h, body, headers); rec.Code != http.StatusAccepted {
						t.Fatalf("status = %d, want 202", rec.Code)
					}
					synctest.Wait()
					want := 1
					if deleted {
						want = 0
						if !strings.Contains(logs.String(), "push ignored: ref was deleted") {
							t.Errorf("missing deletion outcome: %s", &logs)
						}
					}
					if len(pushes) != want {
						t.Fatalf("reconciled %d pushes, want %d", len(pushes), want)
					}
					if want == 1 && (pushes[0].GitRef != ref || pushes[0].Files != nil) {
						t.Errorf("empty non-deletion push must still reconcile its ref: %+v", pushes[0])
					}
					if strings.Contains(logs.String(), `"level":"ERROR"`) {
						t.Errorf("unexpected error: %s", &logs)
					}
					if rec := post(t, h, body, headers); rec.Code != http.StatusOK {
						t.Fatalf("duplicate status = %d, want 200", rec.Code)
					}
					synctest.Wait()
					if len(pushes) != want {
						t.Error("duplicate delivery scheduled work")
					}
				})
			})
		}
	}
}

func TestPushFailureLogsTypeAndTraceWithoutProviderBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		control := &fakeController{push: func(context.Context, controller.Push) error {
			return errors.New("provider response containing private content")
		}}
		h := build(t, setup{store: &fakeStore{creds: sampleCreds()}, control: control,
			logger: slog.New(telemetry.LogHandler(slog.NewJSONHandler(&logs, nil)))})
		const body = `{"ref":"refs/heads/main","repository":{"name":"catalog","owner":{"login":"owner"}},"installation":{"id":2}}`
		req := httptest.NewRequest(http.MethodPost, "/webhooks", strings.NewReader(body))
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", "failed-delivery")
		req.Header.Set("X-Hub-Signature-256", sign(t, body, webhookSecret))
		span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
		req = req.WithContext(trace.ContextWithSpanContext(req.Context(), span))
		h.ServeHTTP(httptest.NewRecorder(), req)
		synctest.Wait()
		for _, want := range []string{`"msg":"reconcile from delivery failed"`, `"level":"ERROR"`, `"error_type":"*errors.errorString"`, `"trace_id":"` + span.TraceID().String() + `"`, `"span_id":"` + span.SpanID().String() + `"`} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("missing %s in %s", want, &logs)
			}
		}
		if strings.Contains(logs.String(), "private content") || strings.Contains(logs.String(), `"error":`) {
			t.Errorf("provider error body leaked: %s", &logs)
		}
	})
}

func TestWebhookHandling(t *testing.T) {
	tests := []struct {
		name       string
		event      string
		delivery   string
		onboarded  bool
		wantStatus int
		wantBody   string
	}{
		{name: "a ping gets a pong", event: "ping", delivery: "d1", onboarded: true, wantStatus: http.StatusOK, wantBody: "pong"},
		{name: "a push is accepted", event: "push", delivery: "d2", onboarded: true, wantStatus: http.StatusAccepted, wantBody: "accepted"},
		{name: "a delivery with no id is rejected", event: "push", onboarded: true, wantStatus: http.StatusBadRequest},
		{
			name:  "a delivery before onboarding is refused rather than dropped silently",
			event: "push", delivery: "d3", wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &fakeStore{}
			if tt.onboarded {
				cs.creds = sampleCreds()
			}
			h := newServer(t, cs, &fakeGitHub{})

			const body = `{"zen":"x"}`
			rec := post(t, h, body, map[string]string{
				"X-GitHub-Event":      tt.event,
				"X-GitHub-Delivery":   tt.delivery,
				"X-Hub-Signature-256": sign(t, body, webhookSecret),
			})

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestWebhookRouteRejectsGet(t *testing.T) {
	h := newServer(t, &fakeStore{creds: sampleCreds()}, &fakeGitHub{})
	if rec := get(t, h, "/webhooks"); rec.Code == http.StatusAccepted {
		t.Error("GET /webhooks should not be accepted as a delivery")
	}
}

// The manifest must point GitHub at the public host while the browser callback
// stays private, or a split-host deployment silently cannot receive deliveries.
func TestADR0005_ManifestUsesPublicHostForWebhooksAndPrivateForCallbacks(t *testing.T) {
	h := newServerWithHosts(t, "https://dusk.internal.example.com", "https://dusk.example.com")
	m := manifestFrom(t, get(t, h, "/setup").Body.String())

	if m.HookAttributes.URL != "https://dusk.example.com/webhooks" {
		t.Errorf("webhook URL = %q, want the public host", m.HookAttributes.URL)
	}
	if m.RedirectURL != "https://dusk.internal.example.com/setup/callback" {
		t.Errorf("redirect URL = %q, want the private host", m.RedirectURL)
	}
}
