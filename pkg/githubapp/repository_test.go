package githubapp_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
)

const testCommit = "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432"

// fakeGitHub serves the endpoints a reconcile uses, and counts the calls so a
// test can assert what was avoided as well as what was fetched.
type fakeGitHub struct {
	files map[string]string

	calls map[string]int
}

func (f *fakeGitHub) start(t *testing.T) *githubapp.Repository {
	t.Helper()
	f.calls = map[string]int{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.Contains(req.URL.Path, "/access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"ghs_secret","expires_at":"2099-01-01T00:00:00Z"}`))

		case strings.Contains(req.URL.Path, "/commits/"):
			f.calls["resolve"]++
			_, _ = w.Write([]byte(`{"sha":"` + testCommit + `"}`))

		case strings.Contains(req.URL.Path, "/contents/"):
			f.calls["contents"]++
			f.writeFile(w, req)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := &githubapp.Client{BaseURL: server.URL}
	return &githubapp.Repository{
		Client:         client,
		Tokens:         &githubapp.Tokens{Client: client, App: pkcs1App(t), Now: time.Now},
		InstallationID: 99,
		Owner:          "example",
		Name:           "homelab",
	}
}

func (f *fakeGitHub) writeFile(w http.ResponseWriter, req *http.Request) {
	name := strings.SplitN(req.URL.Path, "/contents/", 2)[1]
	content, ok := f.files[name]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		return
	}
	_, _ = w.Write([]byte(content))
}

func TestRepositoryResolve(t *testing.T) {
	repo := (&fakeGitHub{}).start(t)

	commit, err := repo.Resolve(t.Context(), "refs/heads/main")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if commit != testCommit {
		t.Errorf("Resolve = %q, want %q", commit, testCommit)
	}
}

func TestRepositoryReadsAreAuthenticated(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.Contains(req.URL.Path, "/access_tokens") {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"ghs_secret","expires_at":"2099-01-01T00:00:00Z"}`))
			return
		}
		auth = req.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"sha":"` + testCommit + `"}`))
	}))
	defer server.Close()

	client := &githubapp.Client{BaseURL: server.URL}
	repo := &githubapp.Repository{
		Client:         client,
		Tokens:         &githubapp.Tokens{Client: client, App: pkcs1App(t)},
		InstallationID: 99,
		Owner:          "example",
		Name:           "homelab",
	}
	if _, err := repo.Resolve(t.Context(), "refs/heads/main"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got, want := auth, "Bearer ghs_secret"; got != want {
		t.Errorf("Authorization = %q, want the installation token", got)
	}
}
