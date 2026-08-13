package githubapp_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
)

// writeServer records what a write actually sent, which is the only way to
// check the parts GitHub silently ignores when they are wrong.
type writeServer struct {
	status int
	body   map[string]any
	method string
	path   string
	reply  string
}

func (w *writeServer) repository(t *testing.T) *githubapp.Repository {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/access_tokens") {
			rw.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(rw, `{"token":"ghs_x","expires_at":"2099-01-01T00:00:00Z"}`)
			return
		}

		w.method, w.path = req.Method, req.URL.Path
		if req.Method == http.MethodPut {
			_ = json.NewDecoder(req.Body).Decode(&w.body)
		}
		if w.status != 0 {
			rw.WriteHeader(w.status)
		}
		_, _ = io.WriteString(rw, w.reply)
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

func TestReadFileContents(t *testing.T) {
	// GitHub wraps base64 at 60 columns, which the strict decoder rejects.
	wrapped := "ZHVzazogdjFhbHBoYTEKa2luZDogc2VydmljZQpuYW1lOiBqZWxseWZpbg==\n"
	server := &writeServer{reply: `{"content":"` + strings.ReplaceAll(wrapped, "\n", `\n`) + `","encoding":"base64","sha":"blob123"}`}

	file, err := server.repository(t).ReadFileContents(t.Context(), "refs/heads/main", "dusk.md")
	if err != nil {
		t.Fatalf("ReadFileContents: %v", err)
	}
	if file.SHA != "blob123" {
		t.Errorf("sha = %q, want blob123", file.SHA)
	}
	if !strings.Contains(string(file.Data), "jellyfin") {
		t.Errorf("data = %q, want the decoded file", file.Data)
	}
}

func TestReadFileContentsReportsAbsence(t *testing.T) {
	server := &writeServer{status: http.StatusNotFound, reply: `{"message":"Not Found"}`}

	_, err := server.repository(t).ReadFileContents(t.Context(), "refs/heads/main", "absent.md")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestCommitFile(t *testing.T) {
	server := &writeServer{
		status: http.StatusOK,
		reply:  `{"commit":{"sha":"c0ffee1","html_url":"https://github.com/example/homelab/commit/c0ffee1"}}`,
	}
	repo := server.repository(t)

	commit, err := repo.CommitFile(t.Context(), githubapp.FileCommit{
		Branch:       "main",
		Path:         "dusk.md",
		Message:      "declare: add service:home/jellyfin",
		Content:      []byte("hello"),
		ReplacingSHA: "blob123",
	})
	if err != nil {
		t.Fatalf("CommitFile: %v", err)
	}

	// An agent must be able to hand a human a link rather than assert success.
	if commit.SHA != "c0ffee1" || !strings.Contains(commit.URL, "commit/c0ffee1") {
		t.Errorf("commit = %+v, want the sha and a browsable url", commit)
	}

	t.Run("it sends what GitHub needs", func(t *testing.T) {
		if server.method != http.MethodPut {
			t.Errorf("method = %s, want PUT", server.method)
		}
		if !strings.HasSuffix(server.path, "/contents/dusk.md") {
			t.Errorf("path = %s, want the contents endpoint", server.path)
		}
		for field, want := range map[string]string{
			"message": "declare: add service:home/jellyfin",
			"branch":  "main",
			"sha":     "blob123",
		} {
			if got, _ := server.body[field].(string); got != want {
				t.Errorf("%s = %q, want %q", field, got, want)
			}
		}
		encoded, _ := server.body["content"].(string)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || string(decoded) != "hello" {
			t.Errorf("content = %q, want base64 of the file", encoded)
		}
	})
}

// Creating must not send a sha. Sending an empty one asks GitHub to replace a
// blob that does not exist, which fails in a way that reads like a bug.
func TestCommitFileOmitsTheShaWhenCreating(t *testing.T) {
	server := &writeServer{status: http.StatusCreated, reply: `{"commit":{"sha":"new1","html_url":"u"}}`}

	_, err := server.repository(t).CommitFile(t.Context(), githubapp.FileCommit{
		Branch: "main", Path: "services/new/dusk.md", Message: "declare: add", Content: []byte("x"),
	})
	if err != nil {
		t.Fatalf("CommitFile: %v", err)
	}
	if _, present := server.body["sha"]; present {
		t.Errorf("a create sent a sha: %v", server.body)
	}
}

// A 409 means the file moved since it was read. That is a collision, and it has
// to read as one rather than as a malformed request.
func TestCommitFileReportsACollision(t *testing.T) {
	server := &writeServer{status: http.StatusConflict, reply: `{"message":"is at abc but expected def"}`}

	_, err := server.repository(t).CommitFile(t.Context(), githubapp.FileCommit{
		Branch: "main", Path: "dusk.md", Message: "declare: update", Content: []byte("x"), ReplacingSHA: "stale",
	})
	if err == nil {
		t.Fatal("CommitFile succeeded on 409, want an error")
	}
	if !strings.Contains(err.Error(), "changed since it was read") {
		t.Errorf("error = %q, want it to name the collision", err)
	}
}

func TestCommitFileRequiresItsFields(t *testing.T) {
	repo := (&writeServer{}).repository(t)

	for _, write := range []githubapp.FileCommit{
		{Path: "dusk.md", Message: "m"},
		{Branch: "main", Message: "m"},
		{Branch: "main", Path: "dusk.md"},
	} {
		if _, err := repo.CommitFile(t.Context(), write); err == nil {
			t.Errorf("CommitFile(%+v) succeeded, want an error", write)
		}
	}
}

func TestDefaultBranch(t *testing.T) {
	server := &writeServer{reply: `{"default_branch":"master"}`}

	branch, err := server.repository(t).DefaultBranch(t.Context())
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if branch != "master" {
		t.Errorf("DefaultBranch = %q, want master", branch)
	}
}
