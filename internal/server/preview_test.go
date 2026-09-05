package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

func previewCatalog(t *testing.T) (*index.DB, string) {
	t.Helper()
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ref := index.PreviewRef("example/catalog", 7)
	for _, version := range []struct{ ref, title string }{{"refs/heads/main", "Live entity"}, {ref, "Preview entity"}} {
		entity := &duskv1alpha1.Entity{Ref: "service:home/example", Kind: "service", Namespace: "home", Name: "example", Title: version.title}
		note := &duskv1alpha1.Note{Id: ".dusk/memo.md", Kind: "note", Body: version.title + " knowledge", Refs: []string{entity.Ref}, ContentHash: version.title}
		if err := db.Put(t.Context(), "example/catalog", version.ref, []index.Declaration{{Path: "dusk.md", Entity: entity}}, nil, []*duskv1alpha1.Note{note}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetDefaultView(t.Context(), "example/catalog", "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	return db, ref
}

func TestPreviewReadsUseTheSnapshotAndWithholdWriteProofs(t *testing.T) {
	db, ref := previewCatalog(t)
	handler := build(t, setup{
		store: registered(), catalog: db, tokens: &proof.Store{},
		pages: declaredPage("---\ntitle: Preview\nblocks:\n  - type: entities\n  - type: recent-notes\n  - type: analytics\n  - type: reads\n  - type: view\n    plugin: example\n---\n"),
		env:   map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})
	for _, path := range []string{"/api/entities/service:home%2Fexample", "/api/notes/.dusk%2Fmemo.md", "/api/notes", "/api/search?q=entity", "/api/home"} {
		t.Run(path, func(t *testing.T) {
			separator := "?"
			if strings.Contains(path, "?") {
				separator = "&"
			}
			rec := get(t, handler, path+separator+"ref="+url.QueryEscape(ref))
			if rec.Code != http.StatusOK {
				t.Fatalf("preview GET = %d: %s", rec.Code, rec.Body.String())
			}
			var answer map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
				t.Fatal(err)
			}
			if _, exists := answer["proof"]; exists {
				t.Error("preview read issued a live write proof")
			}
			body := rec.Body.String()
			if !strings.Contains(body, "Preview entity") || strings.Contains(body, "Live entity") {
				t.Fatalf("preview read mixed live data: %s", body)
			}
			if path == "/api/home" && len(answer["blocks"].([]any)) != 2 {
				t.Fatalf("preview included live-only homepage blocks: %s", body)
			}
		})
	}
	live := get(t, handler, "/api/entities/service:home%2Fexample")
	if !strings.Contains(live.Body.String(), `"proof"`) {
		t.Fatalf("live entity lost its write proof: %s", live.Body.String())
	}
}

func TestPreviewRequestsCannotMutateTheLiveCatalog(t *testing.T) {
	db, ref := previewCatalog(t)
	handler := build(t, setup{store: registered(), catalog: db, env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"}})
	for _, path := range []string{"/api/entities/service:home%2Fexample", "/api/notes", "/api/notes/status", "/api/repository", "/api/context", "/api/plugins/example/install", "/api/entities/service:home%2Fexample/actions/restart"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path+"?ref="+url.QueryEscape(ref), strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Errorf("preview POST %s = %d, want read-only rejection: %s", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{"/api/context", "/api/repository", "/api/plugins"} {
		if rec := get(t, handler, path+"?ref="+url.QueryEscape(ref)); rec.Code != http.StatusConflict {
			t.Errorf("preview opened live editor %s: %d", path, rec.Code)
		}
	}
}

func TestPreviewLinksFailExplicitlyWhenMissingOrAmbiguous(t *testing.T) {
	db, _ := previewCatalog(t)
	handler := build(t, setup{store: registered(), catalog: db, env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"}})
	for _, test := range []struct {
		ref    string
		status int
	}{
		{"refs/pull/7/head", http.StatusOK},
		{index.PreviewRef("example/catalog", 99), http.StatusNotFound},
		{"refs/pull/../../head", http.StatusBadRequest},
	} {
		if rec := get(t, handler, "/api/entities?ref="+url.QueryEscape(test.ref)); rec.Code != test.status {
			t.Errorf("GET preview %s = %d, want %d: %s", test.ref, rec.Code, test.status, rec.Body.String())
		}
	}
	if err := db.Put(t.Context(), "example/other", index.PreviewRef("example/other", 7), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if rec := get(t, handler, "/api/entities?ref=refs/pull/7/head"); rec.Code != http.StatusConflict {
		t.Fatalf("ambiguous preview selected a repository: %d %s", rec.Code, rec.Body.String())
	}
}
