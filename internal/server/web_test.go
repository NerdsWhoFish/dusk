package server_test

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/FetchHQ/dusk/web"
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
