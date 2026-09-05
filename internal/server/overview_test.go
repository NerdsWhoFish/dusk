package server_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"github.com/NerdsWhoFish/dusk/internal/index"
)

func TestOverviewCountsOnlySourcesContributingEntities(t *testing.T) {
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const mainRef = "refs/heads/main"
	observed := index.ObservedScope("example")
	for _, source := range []struct{ repository, name string }{
		{"example/catalog", "declared"},
		{observed, "observed"},
	} {
		entity := &duskv1alpha1.Entity{Ref: "service:home/" + source.name, Kind: "service", Namespace: "home", Name: source.name}
		if err := db.Put(t.Context(), source.repository, mainRef, []index.Declaration{{Path: "dusk.md", Entity: entity}}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.SetDefaultView(t.Context(), source.repository, mainRef); err != nil {
			t.Fatal(err)
		}
	}
	preview := &duskv1alpha1.Entity{Ref: "service:home/proposed", Kind: "service", Namespace: "home", Name: "proposed"}
	if err := db.Put(t.Context(), "example/catalog", index.PreviewRef("example/catalog", 7), []index.Declaration{{Path: "dusk.md", Entity: preview}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDefaultView(t.Context(), "example/not-participating", mainRef); err != nil {
		t.Fatal(err)
	}
	note := &duskv1alpha1.Note{Id: ".dusk/memo.md", Kind: "note", Body: "knowledge without an entity"}
	if err := db.Put(t.Context(), "example/notes-only", mainRef, nil, nil, []*duskv1alpha1.Note{note}); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(t.Context(), "example/empty-preview", index.PreviewRef("example/empty-preview", 7), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	handler := build(t, setup{store: registered(), catalog: db, env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"}})
	rec := get(t, handler, "/api/overview")
	if rec.Code != http.StatusOK {
		t.Fatalf("overview = %d: %s", rec.Code, rec.Body.String())
	}
	var answer struct {
		Declaring int `json:"declaring"`
		Total     int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Declaring != 2 || answer.Total != 2 {
		t.Errorf("overview = %+v, want two entity sources and two entities", answer)
	}
	if scopes, err := db.Scopes(t.Context()); err != nil || len(scopes) != 6 {
		t.Errorf("lifecycle enumeration lost empty/content-only scopes: %+v, error %v", scopes, err)
	}
}
