package index_test

import (
	"errors"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"github.com/NerdsWhoFish/dusk/internal/index"
)

func TestPreviewViewsKeepOtherRepositoriesAndVisibility(t *testing.T) {
	db := newDB(t)
	first, second := "example/first", "example/second"
	for _, repository := range []string{first, second} {
		if err := db.SetDefaultView(t.Context(), repository, mainRef); err != nil {
			t.Fatal(err)
		}
	}
	mustPut(t, db, first, mainRef, []*duskv1alpha1.Entity{entity("service:home/current", "Current", "")}, nil)
	mustPut(t, db, second, mainRef, []*duskv1alpha1.Entity{entity("service:home/unrelated", "Unrelated", "")}, nil)
	ref := index.PreviewRef(first, 7)
	mustPut(t, db, first, ref, []*duskv1alpha1.Entity{entity("service:home/proposed", "Proposed", "")}, nil)
	mustPut(t, db, second, index.PreviewRef(second, 7), []*duskv1alpha1.Entity{entity("service:home/other-preview", "Other preview", "")}, nil)

	entities, err := db.List(t.Context(), ref, "")
	if err != nil || len(entities) != 2 {
		t.Fatalf("preview entities = %+v, error %v", entities, err)
	}
	if _, err := db.Get(t.Context(), ref, "service:home/current"); !errors.Is(err, index.ErrNotFound) {
		t.Errorf("preview retained the replaced default entity: %v", err)
	}
	if _, err := db.Get(t.Context(), ref, "service:home/other-preview"); !errors.Is(err, index.ErrNotFound) {
		t.Errorf("preview included another repository's proposal: %v", err)
	}
	visibility := index.Visibility{Repositories: []string{first}}
	graph, err := db.Graph(t.Context(), ref, visibility)
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Entity.GetRef() != "service:home/proposed" {
		t.Fatalf("preview bypassed graph visibility: %+v, error %v", graph, err)
	}
	results, _, err := db.Search(t.Context(), ref, index.SearchFilter{Query: "Unrelated", Visibility: visibility})
	if err != nil || len(results) != 0 {
		t.Fatalf("preview bypassed search visibility: %+v, error %v", results, err)
	}
}

func TestLegacyPreviewLinksResolveOnlyWhenUnambiguous(t *testing.T) {
	db := newDB(t)
	first, second := "example/first", "example/second"
	if err := db.Put(t.Context(), first, index.PreviewRef(first, 7), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if ref, err := db.ResolvePreview(t.Context(), "refs/pull/7/head"); err != nil || ref != index.PreviewRef(first, 7) {
		t.Fatalf("legacy link = %q, error %v", ref, err)
	}
	if err := db.Put(t.Context(), second, index.PreviewRef(second, 7), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ResolvePreview(t.Context(), "refs/pull/7/head"); !errors.Is(err, index.ErrPreviewAmbiguous) {
		t.Fatalf("ambiguous legacy link silently selected a repository: %v", err)
	}
	if err := db.DropRepository(t.Context(), first, index.PreviewRef(first, 7)); err != nil {
		t.Fatal(err)
	}
	if ref, err := db.ResolvePreview(t.Context(), "refs/pull/7/head"); err != nil || ref != index.PreviewRef(second, 7) {
		t.Fatalf("remaining legacy link = %q, error %v", ref, err)
	}
	if _, err := db.ResolvePreview(t.Context(), index.PreviewRef(first, 7)); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("closed preview still resolves: %v", err)
	}
}

func TestRepositoryRenameMovesItsPreviewScopes(t *testing.T) {
	db := newDB(t)
	if _, err := db.TrackRepository(t.Context(), 42, "example/before"); err != nil {
		t.Fatal(err)
	}
	oldRef := index.PreviewRef("example/before", 7)
	mustPut(t, db, "example/before", oldRef, []*duskv1alpha1.Entity{entity("service:home/proposed", "Proposed", "")}, nil)
	if _, err := db.TrackRepository(t.Context(), 42, "example/after"); err != nil {
		t.Fatal(err)
	}
	newRef := index.PreviewRef("example/after", 7)
	if _, err := db.ResolvePreview(t.Context(), newRef); err != nil {
		t.Fatalf("moved preview is unavailable: %v", err)
	}
	if _, err := db.Get(t.Context(), newRef, "service:home/proposed"); err != nil {
		t.Fatalf("moved preview lost its entities: %v", err)
	}
}
