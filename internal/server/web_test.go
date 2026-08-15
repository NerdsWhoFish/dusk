package server_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/web"
)

// hashedAsset returns a real file from the built bundle. Inventing a name would
// not do: an unknown path falls through to the SPA shell, which never reaches
// the branch under test.
func hashedAsset(t *testing.T) string {
	t.Helper()

	root, ok := web.Bundle()
	if !ok {
		t.Skip("this build has no UI: run `make web`")
	}
	entries, err := fs.ReadDir(root, "assets")
	if err != nil {
		t.Skipf("no hashed assets to check: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return "/assets/" + entry.Name()
		}
	}
	t.Skip("no hashed assets to check")
	return ""
}

// The login and setup pages reference the icons, so an icon behind the gate
// redirects to the page that asked for it and renders nothing. It has to serve
// unauthenticated, and before onboarding, when there is no catalog at all.
func TestIconsServeOutsideTheGate(t *testing.T) {
	for _, test := range []struct {
		name  string
		state setup
	}{
		{"gated", setup{
			store: registered(),
			env:   map[string]string{"DUSK_MCP_TOKEN": "shared"},
		}},
		{"before onboarding", setup{
			env: map[string]string{"DUSK_MCP_TOKEN": "shared"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := build(t, test.state)

			for _, target := range []string{"/favicon.svg", "/apple-touch-icon.png"} {
				rec := get(t, handler, target)
				if rec.Code == http.StatusNotFound {
					t.Skip("this build has no UI: run `make web`")
				}
				if rec.Code != http.StatusOK {
					t.Errorf("GET %s = %d, want 200", target, rec.Code)
				}
				if rec.Body.Len() == 0 {
					t.Errorf("GET %s served nothing", target)
				}
			}
		})
	}
}

// The API mux was mounted as "GET /api/", so every write fell through to the
// browser catch-all and was answered with a redirect to the login page. A
// browser follows it, gets HTML with ok: true, and reports a JSON parse error.
func TestAPIWritesAreNotRedirectedToLogin(t *testing.T) {
	handler := build(t, setup{
		store:   registered(),
		catalog: emptyCatalog(t),
		env:     map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})

	// Every write route, so one added and not mounted fails here rather than in
	// a browser reporting a JSON parse error.
	for _, target := range []string{
		"/api/plugins/kubernetes/install",
		"/api/plugins/kubernetes/uninstall",
		"/api/plugins/kubernetes/restart",
		"/api/plugins/refresh",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, nil))

		if rec.Code == http.StatusSeeOther {
			t.Errorf("POST %s was redirected to %q, so it never reached a handler",
				target, rec.Header().Get("Location"))
		}
		if body := rec.Body.String(); strings.HasPrefix(strings.TrimSpace(body), "<") {
			t.Errorf("POST %s answered with markup rather than JSON: %.60s", target, body)
		}
	}
}

// A file under a fixed name is replaced in place by the next build, so serving
// it immutable pins every browser that ever loaded it to the version it first
// saw. Only Vite's content-hashed output can make that promise.
func TestOnlyHashedAssetsAreImmutable(t *testing.T) {
	// Reaching an asset needs all three: onboarding done, a catalog for the SPA
	// route to exist at all, and something that opens the fail-closed gate.
	handler := build(t, setup{
		store:   registered(),
		catalog: emptyCatalog(t),
		env:     map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})

	for _, test := range []struct {
		name      string
		target    string
		immutable bool
	}{
		{"favicon", "/favicon.svg", false},
		{"touch icon", "/apple-touch-icon.png", false},
		{"hashed bundle", hashedAsset(t), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := get(t, handler, test.target)
			if rec.Code == http.StatusNotImplemented {
				t.Skip("this build has no UI")
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", test.target, rec.Code)
			}

			cache := rec.Header().Get("Cache-Control")
			if got := strings.Contains(cache, "immutable"); got != test.immutable {
				t.Errorf("Cache-Control = %q, immutable = %v, want %v", cache, got, test.immutable)
			}
		})
	}
}
