package index_test

import (
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

const previewRef = "refs/pull/7/head"

// The diff is semantic. A reviewer wants to know what the catalog would say
// after merge, which a file diff cannot tell them.
func TestDiffReportsWhatMergingWouldDo(t *testing.T) {
	db := newDB(t)

	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", "Media server."),
		entity("service:home/retiring", "Retiring", ""),
	}, nil)

	mustPut(t, db, testRepo, previewRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", "Media server, transcoding off."),
		entity("service:home/arriving", "Arriving", ""),
	}, nil)

	changes, err := db.Diff(t.Context(), mainRef, previewRef)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	byRef := map[string]index.Change{}
	for _, change := range changes {
		byRef[change.Ref] = change
	}

	if got := byRef["service:home/arriving"].Kind; got != index.ChangeAdded {
		t.Errorf("arriving = %q, want added", got)
	}
	if got := byRef["service:home/retiring"].Kind; got != index.ChangeRemoved {
		t.Errorf("retiring = %q, want removed", got)
	}

	changed := byRef["service:home/jellyfin"]
	if changed.Kind != index.ChangeModified || changed.Field != "description" {
		t.Errorf("jellyfin = %+v, want a modified description", changed)
	}
	if changed.After != "Media server, transcoding off." {
		t.Errorf("after = %q", changed.After)
	}
}

// A pull request that reformats a file changes nothing about the catalog, and
// saying otherwise trains people to ignore the comment.
func TestDiffIsSilentWhenNothingSemanticChanged(t *testing.T) {
	db := newDB(t)

	same := []*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "Media server.")}
	mustPut(t, db, testRepo, mainRef, same, nil)
	mustPut(t, db, testRepo, previewRef, same, nil)

	changes, err := db.Diff(t.Context(), mainRef, previewRef)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %+v, want none", changes)
	}
}

// Provenance differs between any two refs by construction, so reporting it
// would bury every real change under noise.
func TestDiffIgnoresProvenance(t *testing.T) {
	db := newDB(t)

	before := entity("service:home/jellyfin", "Jellyfin", "Media server.")
	before.Provenance = &duskv1alpha1.Provenance{Source: "dusk.md", Version: "aaaaaaa"}
	after := entity("service:home/jellyfin", "Jellyfin", "Media server.")
	after.Provenance = &duskv1alpha1.Provenance{Source: "dusk.md", Version: "bbbbbbb"}

	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{before}, nil)
	mustPut(t, db, testRepo, previewRef, []*duskv1alpha1.Entity{after}, nil)

	changes, err := db.Diff(t.Context(), mainRef, previewRef)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %+v, want none: only the commit differs", changes)
	}
}

// An attribute is where most of what a catalog knows actually lives.
func TestDiffReportsAttributeChanges(t *testing.T) {
	db := newDB(t)

	before := entity("service:home/jellyfin", "Jellyfin", "")
	before.Attributes = attributes(t, map[string]any{"backup": "nightly"})
	after := entity("service:home/jellyfin", "Jellyfin", "")
	after.Attributes = attributes(t, map[string]any{"backup": "weekly", "url": "https://x"})

	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{before}, nil)
	mustPut(t, db, testRepo, previewRef, []*duskv1alpha1.Entity{after}, nil)

	changes, err := db.Diff(t.Context(), mainRef, previewRef)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("changes = %+v, want the changed one and the added one", changes)
	}
}
