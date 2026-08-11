package controller_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FetchHQ/dusk/internal/controller"
	"github.com/FetchHQ/dusk/internal/index"
	"github.com/FetchHQ/dusk/internal/store"
	"github.com/FetchHQ/dusk/pkg/githubapp"
	"github.com/FetchHQ/dusk/pkg/secret"
)

const mainRef = "refs/heads/main"

func rootFile(name string) string {
	return fmt.Sprintf(`---
dusk: v1alpha1
namespace: home
kind: service
name: %s
---

Declared by %s.
`, name, name)
}

// install is one installation the fake GitHub will report.
type install struct {
	id      int64
	account string
	repos   map[string]string

	listFails bool
}

// fakeGitHub serves enough of the API for a whole sweep.
type fakeGitHub struct {
	installs []install

	mu    sync.Mutex
	calls map[string]int
}

func (f *fakeGitHub) start(t *testing.T) *githubapp.Client {
	t.Helper()
	f.calls = map[string]int{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		f.count(req.URL.Path)
		switch {
		case strings.HasSuffix(req.URL.Path, "/access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"token":"ghs_x","expires_at":"2099-01-01T00:00:00Z"}`)
		case req.URL.Path == "/app/installations":
			f.writeInstallations(w)
		case req.URL.Path == "/installation/repositories":
			f.writeRepositories(w, req)
		case strings.Contains(req.URL.Path, "/commits/"):
			_, _ = io.WriteString(w, `{"sha":"abc1234def5678"}`)
		case strings.Contains(req.URL.Path, "/git/trees/"):
			_, _ = io.WriteString(w, `{"truncated":false,"tree":[]}`)
		case strings.Contains(req.URL.Path, "/contents/"):
			f.writeContents(w, req)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return &githubapp.Client{BaseURL: server.URL}
}

func (f *fakeGitHub) count(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[path]++
}

func (f *fakeGitHub) writeInstallations(w http.ResponseWriter) {
	out := make([]map[string]any, 0, len(f.installs))
	for _, i := range f.installs {
		out = append(out, map[string]any{"id": i.id, "account": map[string]any{"login": i.account}})
	}
	_ = json.NewEncoder(w).Encode(out)
}

// writeRepositories answers for whichever installation the token belongs to.
// The fake cannot tell them apart from the token, so a test uses one
// installation per repository set, or marks one as failing.
func (f *fakeGitHub) writeRepositories(w http.ResponseWriter, _ *http.Request) {
	for _, i := range f.installs {
		if i.listFails {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"upstream is having a day"}`)
			return
		}
	}

	repos := make([]map[string]any, 0)
	for _, i := range f.installs {
		for slug := range i.repos {
			owner, name, _ := strings.Cut(slug, "/")
			repos = append(repos, map[string]any{
				"name":           name,
				"default_branch": "main",
				"owner":          map[string]any{"login": owner},
			})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
}

func (f *fakeGitHub) writeContents(w http.ResponseWriter, req *http.Request) {
	slug := strings.TrimPrefix(strings.SplitN(req.URL.Path, "/contents/", 2)[0], "/repos/")
	for _, i := range f.installs {
		if content, ok := i.repos[slug]; ok {
			_, _ = io.WriteString(w, content)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"message":"Not Found"}`)
}

// fakeCredentials stands in for the encrypted store.
type fakeCredentials struct{ creds *store.Credentials }

func (f fakeCredentials) Load() (*store.Credentials, error) { return f.creds, nil }

func newController(t *testing.T, fake *fakeGitHub, owner string, opts controller.Options) (*controller.Controller, *index.DB) {
	t.Helper()

	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	opts.Index = idx
	opts.Client = fake.start(t)
	opts.Credentials = fakeCredentials{creds: &store.Credentials{
		AppID: 1,
		Owner: owner,
		PrivateKey: secret.New(string(pem.EncodeToMemory(
			&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))),
	}}
	opts.Logger = slog.New(slog.DiscardHandler)

	c, err := controller.New(opts)
	if err != nil {
		t.Fatalf("controller.New: %v", err)
	}
	return c, idx
}

func TestSyncReconcilesEveryRepository(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "example",
		repos: map[string]string{
			"example/homelab": rootFile("jellyfin"),
			"example/media":   rootFile("navidrome"),
		},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for _, ref := range []string{"service:home/jellyfin", "service:home/navidrome"} {
		if _, err := idx.Get(t.Context(), mainRef, ref); err != nil {
			t.Errorf("Get(%q): %v", ref, err)
		}
	}

	t.Run("status reports each repository", func(t *testing.T) {
		statuses := c.Status()
		if len(statuses) != 2 {
			t.Fatalf("Status = %d entries, want 2", len(statuses))
		}
		for _, s := range statuses {
			if s.Error != "" {
				t.Errorf("%s reported %q", s.Repository, s.Error)
			}
			if s.Commit == "" {
				t.Errorf("%s recorded no commit", s.Repository)
			}
		}
	})
}

// A GitHub App can be installed by anyone able to see it. An uninvited
// installation must never reach the catalog, because its markdown becomes
// context agents treat as fact.
func TestUninvitedInstallationIsNeverReconciled(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      66,
		account: "stranger",
		repos:   map[string]string{"stranger/malicious": rootFile("backdoor")},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if _, err := idx.Get(t.Context(), mainRef, "service:home/backdoor"); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("an unallowed account reached the catalog: %v", err)
	}
	if scopes, _ := idx.Scopes(t.Context()); len(scopes) != 0 {
		t.Errorf("index holds %v, want nothing", scopes)
	}

	t.Run("a webhook for that account is refused too", func(t *testing.T) {
		if err := c.SyncRepository(t.Context(), 66, "stranger", "stranger", "malicious", mainRef); err != nil {
			t.Fatalf("SyncRepository: %v", err)
		}
		if scopes, _ := idx.Scopes(t.Context()); len(scopes) != 0 {
			t.Errorf("a delivery bypassed the allowlist: %v", scopes)
		}
	})
}

// The App's owner and a repository's owner are different things, and they
// coincide often enough that confusing them passes most tests. An App owned by
// a person and installed on an organisation is where it shows.
func TestRepositoryOwnerIsNotTheAppOwner(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "acme",
		repos:   map[string]string{"acme/platform": rootFile("jellyfin")},
	}}}
	c, idx := newController(t, fake, "a-person", controller.Options{Accounts: []string{"acme"}})

	if err := c.SyncRepository(t.Context(), 10, "acme", "acme", "platform", mainRef); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}

	scopes, err := idx.Scopes(t.Context())
	if err != nil {
		t.Fatalf("Scopes: %v", err)
	}
	if len(scopes) != 1 || scopes[0].Repository != "acme/platform" {
		t.Fatalf("stored under %v, want acme/platform", scopes)
	}
}

func TestAllowlist(t *testing.T) {
	tests := []struct {
		name     string
		owner    string
		accounts []string
		account  string
		want     bool
	}{
		{"the App owner is allowed by default", "example", nil, "example", true},
		{"any other account is refused by default", "example", nil, "stranger", false},
		{"logins compare case insensitively", "Example", nil, "eXaMpLe", true},
		{"an explicit list is honoured", "example", []string{"trusted"}, "trusted", true},
		{"an explicit list excludes the owner unless listed", "example", []string{"trusted"}, "example", false},
		{"an empty account is never allowed", "", nil, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newController(t, &fakeGitHub{}, tt.owner, controller.Options{
				Accounts: tt.accounts,
			})
			if got := c.Permitted(tt.account, tt.owner); got != tt.want {
				t.Errorf("Permitted(%q) = %v, want %v", tt.account, got, tt.want)
			}
		})
	}
}

func TestSyncPrunesRepositoriesItCanNoLongerSee(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "example",
		repos: map[string]string{
			"example/homelab": rootFile("jellyfin"),
			"example/media":   rootFile("navidrome"),
		},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	delete(fake.installs[0].repos, "example/media")
	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if _, err := idx.Get(t.Context(), mainRef, "service:home/navidrome"); !errors.Is(err, index.ErrNotFound) {
		t.Errorf("a revoked repository kept its entities: %v", err)
	}
	if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err != nil {
		t.Errorf("a still-installed repository was pruned: %v", err)
	}
}

// ADR-0011's rule generalised: "I could not look" must never be mistaken for
// "it is not there". A sweep that failed to enumerate must not delete.
func TestADR0011_AnIncompleteSweepNeverPrunes(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "example",
		repos:   map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	fake.installs[0].listFails = true
	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync returned an error rather than carrying on: %v", err)
	}

	if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err != nil {
		t.Errorf("a failed sweep deleted the previous graph: %v", err)
	}
}

func TestRunSweepsUntilCancelled(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "example",
		repos:   map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{Interval: time.Hour})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()

	// Run sweeps immediately rather than waiting out the first tick, so the
	// catalog is current as soon as the process is.
	deadline := time.After(10 * time.Second)
	for {
		if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not sweep before its first tick")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
}
