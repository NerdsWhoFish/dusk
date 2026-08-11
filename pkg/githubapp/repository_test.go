package githubapp_test

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FetchHQ/dusk/pkg/githubapp"
)

const testCommit = "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432"

// fakeGitHub serves the three endpoints a reconcile uses, and counts the calls
// so a test can assert what was avoided as well as what was fetched.
type fakeGitHub struct {
	files     map[string]string
	tree      []string
	truncated bool

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

		case strings.Contains(req.URL.Path, "/git/trees/"):
			f.calls["tree"]++
			f.writeTree(w)

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

func (f *fakeGitHub) writeTree(w http.ResponseWriter) {
	entries := make([]string, 0, len(f.tree))
	for _, p := range f.tree {
		entries = append(entries, `{"path":"`+p+`","type":"blob"}`)
	}
	entries = append(entries, `{"path":"services","type":"tree"}`)
	truncated := "false"
	if f.truncated {
		truncated = "true"
	}
	_, _ = w.Write([]byte(`{"truncated":` + truncated + `,"tree":[` + strings.Join(entries, ",") + `]}`))
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

func TestRepositoryReadFile(t *testing.T) {
	repo := (&fakeGitHub{files: map[string]string{"dusk.md": "the catalog file"}}).start(t)

	t.Run("a file is returned verbatim", func(t *testing.T) {
		data, err := repo.ReadFile(t.Context(), testCommit, "dusk.md")
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if got, want := string(data), "the catalog file"; got != want {
			t.Errorf("ReadFile = %q, want %q", got, want)
		}
	})

	// A repository with no dusk.md has not opted in, and the reconciler tells
	// that from a real failure by this error alone.
	t.Run("a missing file reports as not existing", func(t *testing.T) {
		_, err := repo.ReadFile(t.Context(), testCommit, "absent.md")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile = %v, want fs.ErrNotExist", err)
		}
	})
}

func TestRepositoryGlob(t *testing.T) {
	fake := &fakeGitHub{tree: []string{
		"dusk.md",
		"README.md",
		"services/jellyfin/dusk.md",
		"services/navidrome/dusk.md",
		"services/jellyfin/notes.md",
		"datastores/postgres/dusk.md",
	}}
	repo := fake.start(t)

	matches, err := repo.Glob(t.Context(), testCommit, "services/*/dusk.md")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	want := []string{"services/jellyfin/dusk.md", "services/navidrome/dusk.md"}
	if strings.Join(matches, ",") != strings.Join(want, ",") {
		t.Errorf("Glob = %v, want %v", matches, want)
	}

	t.Run("the tree is fetched once per commit", func(t *testing.T) {
		if _, err := repo.Glob(t.Context(), testCommit, "datastores/*/dusk.md"); err != nil {
			t.Fatalf("Glob: %v", err)
		}
		if fake.calls["tree"] != 1 {
			t.Errorf("listed the tree %d times, want 1 for an immutable commit", fake.calls["tree"])
		}
	})
}

// A truncated listing would drop catalog files, producing a catalog that is
// confidently incomplete. Failing is the only safe response.
func TestRepositoryRejectsATruncatedTree(t *testing.T) {
	repo := (&fakeGitHub{
		tree:      []string{"dusk.md"},
		truncated: true,
	}).start(t)

	_, err := repo.Glob(t.Context(), testCommit, "*.md")
	if err == nil {
		t.Fatal("Glob succeeded on a truncated tree, want an error")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %q, want it to explain the repository is too large", err)
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
		_, _ = w.Write([]byte("content"))
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
	if _, err := repo.ReadFile(t.Context(), testCommit, "dusk.md"); err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if got, want := auth, "Bearer ghs_secret"; got != want {
		t.Errorf("Authorization = %q, want the installation token", got)
	}
}
