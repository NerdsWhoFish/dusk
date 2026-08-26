package server_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/contextconfig"
	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

type fixedAgentContext struct {
	preview mcp.ContextPreview
	root    string
}

func (f *fixedAgentContext) PreviewContext(_ context.Context, root string) (mcp.ContextPreview, error) {
	f.root = root
	return f.preview, nil
}

type memoryContextFile struct {
	body    []byte
	missing bool
	token   string
	written []byte
}

func (f *memoryContextFile) Context(context.Context) ([]byte, error) {
	if f.missing {
		return nil, fs.ErrNotExist
	}
	return f.body, nil
}

func (f *memoryContextFile) SetContext(_ context.Context, token string, body []byte) (*write.Result, error) {
	f.token, f.written = token, body
	return &write.Result{
		Ref: contextconfig.Path, Repository: "example/config", Path: contextconfig.Path,
		Commit: "c0ffee", URL: "https://github.com/example/config/commit/c0ffee",
	}, nil
}

type recordingNotes struct {
	notes []write.Note
	token []string
}

func (f *recordingNotes) Record(_ context.Context, token string, note write.Note) (*write.Result, error) {
	f.token = append(f.token, token)
	f.notes = append(f.notes, note)
	return &write.Result{
		Ref: note.Id, Repository: "example/config", Path: note.Id,
		Commit: "c0ffee", URL: "https://github.com/example/config/commit/c0ffee", Removed: note.Remove,
	}, nil
}

func (*recordingNotes) NoteDestination() string { return "example/config" }

type recordingRepositoryFiles struct {
	file    *write.RepositoryFile
	token   string
	written []byte
}

func (f *recordingRepositoryFiles) RepositoryRoot(context.Context, string) (*write.RepositoryFile, error) {
	return f.file, nil
}

func (f *recordingRepositoryFiles) SetRepositoryRoot(_ context.Context, token, repository string, body []byte) (*write.Result, error) {
	f.token, f.written = token, body
	return &write.Result{
		Ref: repository, Repository: repository, Path: write.RootFile, Created: !f.file.Exists,
		Commit: "c0ffee", URL: "https://github.com/example/homelab/commit/c0ffee",
	}, nil
}

func postAPI(t *testing.T, handler http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	return rec
}

func TestContextAPIUsesTheAgentRendererAndReturnsTheEditableProfile(t *testing.T) {
	agent := &fixedAgentContext{preview: mcp.ContextPreview{
		Repository: "example/homelab", Declared: []string{"service:home/jellyfin"},
		EntityCount: 42, Budget: 8000, Context: "# Dusk context\n\nExact agent payload.\n",
	}}
	profile := &memoryContextFile{body: []byte(`---
dusk: context/v1
budget: 8000
inventory: full
---
Read every pinned note.
`)}
	tokens := &proof.Store{}
	handler := build(t, setup{
		store: registered(), catalog: emptyCatalog(t), context: agent, profile: profile, tokens: tokens,
		env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})

	rec := get(t, handler, "/api/context?root=example%2Fhomelab")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET context = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var answer struct {
		Context    string   `json:"context"`
		Repository string   `json:"repository"`
		Declared   []string `json:"declared"`
		Budget     int      `json:"budget"`
		Bytes      int      `json:"bytes"`
		Profile    struct {
			Body  string `json:"body"`
			Proof string `json:"proof"`
			Path  string `json:"path"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	if agent.root != "example/homelab" || answer.Context != agent.preview.Context {
		t.Fatalf("root = %q, context = %q", agent.root, answer.Context)
	}
	if answer.Repository != agent.preview.Repository || answer.Budget != agent.preview.Budget || answer.Bytes != len(agent.preview.Context) {
		t.Fatalf("answer = %+v", answer)
	}
	if answer.Profile.Path != contextconfig.Path || answer.Profile.Proof == "" || answer.Profile.Body != string(profile.body) {
		t.Fatalf("profile = %+v", answer.Profile)
	}

	replacement := strings.Replace(answer.Profile.Body, "budget: 8000", "budget: 12000", 1)
	payload, err := json.Marshal(map[string]string{"body": replacement, "proof": answer.Profile.Proof})
	if err != nil {
		t.Fatalf("encode context update: %v", err)
	}
	rec = postAPI(t, handler, "/api/context", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST context = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if profile.token != answer.Profile.Proof || string(profile.written) != replacement {
		t.Fatalf("token = %q, written = %q", profile.token, profile.written)
	}
}

func TestContextAPIRequiresItsWriteConfiguration(t *testing.T) {
	handler := build(t, setup{
		store: registered(), catalog: emptyCatalog(t),
		context: &fixedAgentContext{preview: mcp.ContextPreview{Context: "private"}},
	})
	rec := get(t, handler, "/api/context")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET context = %d, want unavailable: %s", rec.Code, rec.Body.String())
	}
}

func TestNotesAPIListsAndMutatesCatalogNotes(t *testing.T) {
	db := emptyCatalog(t)
	note := &duskv1alpha1.Note{
		Id: ".dusk/gotcha-storage.md", Kind: "gotcha", Body: "Keep the volume mounted.",
		Refs: []string{"service:home/jellyfin"}, Pinned: true, ContentHash: "note-version",
	}
	entity := &duskv1alpha1.Entity{
		Ref: "service:home/jellyfin", Kind: "service", Namespace: "home", Name: "jellyfin",
	}
	declarations := []index.Declaration{{Entity: entity}}
	if err := db.(*index.DB).Put(t.Context(), "example/config", "refs/heads/main", declarations, nil, []*duskv1alpha1.Note{note}); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	if err := db.(*index.DB).SetDefaultView(t.Context(), "example/config", "refs/heads/main"); err != nil {
		t.Fatalf("set default view: %v", err)
	}
	writes := &recordingNotes{}
	handler := build(t, setup{
		store: registered(), catalog: db, notes: writes, tokens: &proof.Store{},
		env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})

	rec := get(t, handler, "/api/notes?limit=25")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET notes = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Notes []struct {
			ID     string   `json:"id"`
			Refs   []string `json:"refs"`
			Pinned bool     `json:"pinned"`
		} `json:"notes"`
		Total int    `json:"total"`
		Proof string `json:"proof"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode notes: %v", err)
	}
	if page.Total != 1 || len(page.Notes) != 1 || page.Notes[0].ID != note.GetId() || !page.Notes[0].Pinned {
		t.Fatalf("page = %+v", page)
	}
	if len(page.Notes[0].Refs) != 1 || page.Notes[0].Refs[0] != note.GetRefs()[0] || page.Proof == "" {
		t.Fatalf("page = %+v", page)
	}

	rec = get(t, handler, "/api/notes?repository=example%2Fconfig")
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.Total != 1 {
		t.Fatalf("repository note page = %+v, error = %v", page, err)
	}
	rec = get(t, handler, "/api/notes?repository=example%2Funrelated")
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.Total != 0 {
		t.Fatalf("unrelated repository note page = %+v, error = %v", page, err)
	}

	update, _ := json.Marshal(map[string]any{
		"id": note.GetId(), "pinned": false, "proof": page.Proof,
	})
	rec = postAPI(t, handler, "/api/notes", string(update))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST note = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	remove, _ := json.Marshal(map[string]any{
		"id": note.GetId(), "confirm": true, "proof": page.Proof,
	})
	rec = postAPI(t, handler, "/api/notes/delete", string(remove))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE note = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(writes.notes) != 2 || writes.notes[0].Pinned == nil || *writes.notes[0].Pinned || !writes.notes[1].Remove || !writes.notes[1].Confirm {
		t.Fatalf("writes = %+v", writes.notes)
	}
}

func TestRepositoryAPIReadsAndCreatesDuskMD(t *testing.T) {
	files := &recordingRepositoryFiles{file: &write.RepositoryFile{
		Repository: "example/homelab", Path: write.RootFile,
		Template: []byte("---\ndusk: v1alpha1\nnamespace: example\nkind: repository\nname: homelab\n---\n"),
	}}
	handler := build(t, setup{
		store: registered(), catalog: emptyCatalog(t), repositories: files, tokens: &proof.Store{},
		env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})

	rec := get(t, handler, "/api/repository?repository=example%2Fhomelab")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET repository = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var read struct {
		Template string `json:"template"`
		Proof    string `json:"proof"`
		Declared bool   `json:"declared"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode repository: %v", err)
	}
	if read.Declared || read.Template == "" || read.Proof == "" {
		t.Fatalf("read = %+v, want an undeclared editable file and proof", read)
	}

	payload, _ := json.Marshal(map[string]any{
		"repository": "example/homelab", "body": read.Template, "proof": read.Proof,
	})
	rec = postAPI(t, handler, "/api/repository", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST repository = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if files.token != read.Proof || string(files.written) != read.Template {
		t.Fatalf("token = %q, body = %q", files.token, files.written)
	}
}
