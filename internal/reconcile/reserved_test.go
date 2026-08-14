package reconcile_test

import (
	"testing"

	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
)

const homeFile = `---
blocks:
  - type: kinds
    title: The estate
---

Everything at a glance.
`

const kindsFile = `---
dusk: v1alpha1
entities:
  - name: airport
    role: reference
---

Airports are reference data.
`

// A reserved file is markdown in the directory a reconcile always reads, so
// before ADR-0048 a declared homepage parsed as an entity and took its whole
// repository's reconcile down with it.
func TestADR0048_ReservedFilesAreNotCatalogContent(t *testing.T) {
	for _, tt := range []struct {
		name string
		path string
		body string
	}{
		{name: "the portal page", path: catalogfs.HomePath, body: homeFile},
		{name: "the kind vocabulary", path: catalogfs.KindsPath, body: kindsFile},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, reconciler := setup(t, map[string]string{
				"dusk.md": rootFile,
				tt.path:   tt.body,
			})

			graph, err := reconciler.Reconcile(t.Context(), testRepo, mainRef, observedAt)
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if len(graph.Entities) != 1 {
				t.Errorf("Entities = %d, want 1: %s contributed one", len(graph.Entities), tt.path)
			}
			if len(graph.Notes) != 0 {
				t.Errorf("Notes = %d, want 0: %s was read as a note", len(graph.Notes), tt.path)
			}
		})
	}
}

// The skip is by exact path, so everything else in `.dusk/` is still content.
func TestReservedSkipDoesNotHideOtherDuskFiles(t *testing.T) {
	_, reconciler := setup(t, map[string]string{
		"dusk.md":                 rootFile,
		catalogfs.HomePath:        homeFile,
		".dusk/gotcha-abc123.md":  noteFile,
		".dusk/deep/runbook-1.md": noteFile,
	})

	graph, err := reconciler.Reconcile(t.Context(), testRepo, mainRef, observedAt)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(graph.Notes) != 2 {
		t.Errorf("Notes = %d, want 2", len(graph.Notes))
	}
}
