package server_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	h := newServerWithHosts(t, "https://dusk.stout.zone", "https://dusk.example.com")
	m := manifestFrom(t, get(t, h, "/setup").Body.String())

	if m.HookAttributes.URL != "https://dusk.example.com/webhooks" {
		t.Errorf("webhook URL = %q, want the public host", m.HookAttributes.URL)
	}
	if m.RedirectURL != "https://dusk.stout.zone/setup/callback" {
		t.Errorf("redirect URL = %q, want the private host", m.RedirectURL)
	}
}
