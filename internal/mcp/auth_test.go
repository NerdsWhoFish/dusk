package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

func reached() (http.Handler, *bool) {
	got := new(bool)
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*got = true
		w.WriteHeader(http.StatusOK)
	}), got
}

func TestRequireBearer(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   int
	}{
		{"the right token is let through", "Bearer s3cret", http.StatusOK},
		{"a wrong token is refused", "Bearer wrong", http.StatusUnauthorized},
		{"no header is refused", "", http.StatusUnauthorized},
		{"the scheme is required", "s3cret", http.StatusUnauthorized},
		{"a prefix of the token is not enough", "Bearer s3c", http.StatusUnauthorized},
		{"the token is not a prefix match either", "Bearer s3cretmore", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, arrived := reached()
			handler := mcp.RequireBearer(next, secret.New("s3cret"))

			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if *arrived != (tt.want == http.StatusOK) {
				t.Errorf("handler reached = %v, want %v", *arrived, tt.want == http.StatusOK)
			}
		})
	}

	t.Run("a refusal says how to authenticate", func(t *testing.T) {
		next, _ := reached()
		rec := httptest.NewRecorder()
		mcp.RequireBearer(next, secret.New("s3cret")).
			ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))

		if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
			t.Errorf("WWW-Authenticate = %q, want a Bearer challenge", got)
		}
	})
}

// Off is a decision, and a bare 404 would read as a bug in Dusk rather than as
// a deployment that has not said who may read the catalog.
func TestDisabledExplainsItself(t *testing.T) {
	rec := httptest.NewRecorder()
	mcp.Disabled().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	for _, want := range []string{"DUSK_MCP_TOKEN", "DUSK_TRUSTED_NETWORK"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q, so nobody learns how to turn it on:\n%s", want, rec.Body.String())
		}
	}
}
