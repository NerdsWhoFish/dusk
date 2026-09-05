package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

func TestUninstallRejectsDecodedPathComponents(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "credentials.enc")
	if err := os.WriteFile(marker, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{plugins: &plugin.Manager{Store: &plugin.Store{Dir: filepath.Join(root, "plugins")}}}
	for _, encoded := range []string{"%2e%2e", "%2e%2e%2foutside", "%2fabsolute", "%2e"} {
		t.Run(encoded, func(t *testing.T) {
			response := httptest.NewRecorder()
			s.apiRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/plugins/"+encoded+"/uninstall", http.NoBody))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid plugin path returned %d: %s", response.Code, response.Body.String())
			}
			if body, err := os.ReadFile(marker); err != nil || string(body) != "preserved" {
				t.Fatalf("uninstall changed parent data: %q, %v", body, err)
			}
		})
	}
}
