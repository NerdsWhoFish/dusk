package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/FetchHQ/dusk/internal/store"
	"github.com/FetchHQ/dusk/pkg/secret"
)

const signInButton = `href="/auth/github"`

// A login page is only reached when there is something to get past, so every
// test here needs the shared token set.
func loginPage(t *testing.T, cs *fakeStore, env map[string]string) string {
	t.Helper()
	if env == nil {
		env = map[string]string{}
	}
	env["DUSK_MCP_TOKEN"] = "shared"

	handler := build(t, cs, &fakeGitHub{}, externalURL, "", env)
	rec := get(t, handler, "/login")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

func registered() *fakeStore {
	return &fakeStore{creds: &store.Credentials{
		AppID:        1,
		Owner:        "acme",
		ClientID:     "Iv1.app",
		ClientSecret: secret.New("app-secret"),
	}}
}

// Registering the App is the only setup step there is, and GitHub hands back
// OAuth credentials as part of it. Asking the operator to create a second app
// and copy in credentials Dusk already holds is a step that should not exist.
func TestSignInIsOfferedFromTheRegisteredApp(t *testing.T) {
	if body := loginPage(t, registered(), nil); !strings.Contains(body, signInButton) {
		t.Error("no GitHub sign-in offered although an App is registered")
	}
}

// Before setup there are no credentials at all, and a button that leads to a
// broken exchange is worse than no button.
func TestSignInIsHiddenBeforeAnAppIsRegistered(t *testing.T) {
	if body := loginPage(t, &fakeStore{}, nil); strings.Contains(body, signInButton) {
		t.Error("GitHub sign-in offered with no credentials to sign in with")
	}
}

// The override exists to point a deployment at an app it did not register.
func TestSignInPrefersTheConfiguredApp(t *testing.T) {
	body := loginPage(t, registered(), map[string]string{
		"DUSK_GITHUB_CLIENT_ID":     "Iv1.configured",
		"DUSK_GITHUB_CLIENT_SECRET": "configured-secret",
	})
	if !strings.Contains(body, signInButton) {
		t.Fatal("no GitHub sign-in offered although one is configured")
	}

	rec := get(t, build(t, registered(), &fakeGitHub{}, externalURL, "", map[string]string{
		"DUSK_MCP_TOKEN":            "shared",
		"DUSK_GITHUB_CLIENT_ID":     "Iv1.configured",
		"DUSK_GITHUB_CLIENT_SECRET": "configured-secret",
	}), "/auth/github")

	target := rec.Header().Get("Location")
	if !strings.Contains(target, "client_id=Iv1.configured") {
		t.Errorf("sign-in went to %q, want the configured client id", target)
	}
}

// The App is registered after the process starts, so resolving credentials once
// at construction would leave sign-in dark until the next restart.
func TestSignInAppearsWithoutARestart(t *testing.T) {
	cs := &fakeStore{}
	handler := build(t, cs, &fakeGitHub{}, externalURL, "", map[string]string{
		"DUSK_MCP_TOKEN": "shared",
	})

	if body := get(t, handler, "/login").Body.String(); strings.Contains(body, signInButton) {
		t.Fatal("GitHub sign-in offered before an App was registered")
	}

	cs.creds = registered().creds

	if body := get(t, handler, "/login").Body.String(); !strings.Contains(body, signInButton) {
		t.Error("GitHub sign-in still hidden after the App was registered")
	}
}
