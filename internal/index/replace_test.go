package index_test

import (
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

// A reconcile replaces everything a repository contributes. Anything left
// behind outlives the file that made it, and stays searchable forever.
func TestPutRemovesEverythingTheScopeHeld(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	err := db.Put(ctx, testRepo, mainRef,
		declare([]*duskv1alpha1.Entity{
			entity("service:home/jellyfin", "Jellyfin", ""),
			entity("service:home/going", "Going away", ""),
		}),
		[]*duskv1alpha1.Relation{relation("service:home/jellyfin", "service:home/going", "talks_to")},
		[]*duskv1alpha1.Note{{
			Id: ".dusk/gone.md", Kind: "gotcha", Body: "this note is deleted next",
			Refs: []string{"service:home/jellyfin"},
		}},
	)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// The second reconcile of a repository where both the note and one entity
	// were deleted.
	err = db.Put(ctx, testRepo, mainRef,
		declare([]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")}),
		nil, nil,
	)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}

	t.Run("the deleted entity is gone", func(t *testing.T) {
		if _, err := db.Get(ctx, mainRef, "service:home/going"); err == nil {
			t.Error("an entity removed from the repository is still in the catalog")
		}
	})

	t.Run("the deleted note is gone", func(t *testing.T) {
		notes, err := db.NotesFor(ctx, mainRef, "service:home/jellyfin")
		if err != nil {
			t.Fatalf("NotesFor: %v", err)
		}
		if len(notes) != 0 {
			t.Errorf("a note removed from the repository is still attached: %+v", notes)
		}
	})

	t.Run("and it is no longer searchable", func(t *testing.T) {
		results, _, err := db.Search(ctx, mainRef, index.SearchFilter{Query: "deleted", Limit: 10})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		for _, result := range results {
			if result.Ref == ".dusk/gone.md" {
				t.Error("a deleted note is still in the search index")
			}
		}
	})

	t.Run("and the relation went with it", func(t *testing.T) {
		relations, err := db.Neighbors(ctx, mainRef, "service:home/jellyfin")
		if err != nil {
			t.Fatalf("Neighbors: %v", err)
		}
		if len(relations) != 0 {
			t.Errorf("a relation removed from the repository survived: %+v", relations)
		}
	})
}
