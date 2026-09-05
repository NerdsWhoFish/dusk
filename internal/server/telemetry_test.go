package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/config"
	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

func TestTelemetryConfigExposesOnlyPublicSettings(t *testing.T) {
	s := &Server{cfg: &config.Config{
		FaroURL: "https://collector.example/collect/public-id", Environment: "production",
		MCPToken: secret.New("private-token"), EncryptionKey: secret.New("private-key"),
	}}
	w := httptest.NewRecorder()
	s.handleTelemetryConfig(w, httptest.NewRequest("GET", "/telemetry/config", nil))
	var payload map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 || payload["url"] != s.cfg.FaroURL || payload["environment"] != s.cfg.Environment {
		t.Fatalf("unexpected public config: %v", payload)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("runtime config may be cached across configuration changes")
	}
}
