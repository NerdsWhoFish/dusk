package index_test

import (
	"slices"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

func seedTwoRepositories(t *testing.T, db *index.DB) {
	t.Helper()
	mustPut(t, db, "example/public", mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/open", "Open", "")}, nil)
	mustPut(t, db, "example/private", mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/secret", "Secret", "")}, nil)
	observe(t, db, "kubernetes", entity("service:cluster/seen", "Seen", ""))
}

// ADR-0012: authorization is derived from GitHub, not configured in Dusk.
// A viewer sees the entities backed by repositories they can read, and
// nothing else.
func TestVisibilityFollowsRepositoryAccess(t *testing.T) {
	db := newDB(t)
	seedTwoRepositories(t, db)

	refs, err := db.VisibleTo(t.Context(), mainRef, index.Visibility{
		Repositories: []string{"example/public"},
	})
	if err != nil {
		t.Fatalf("VisibleTo: %v", err)
	}

	if !slices.Contains(refs, "service:home/open") {
		t.Errorf("refs = %v, want the readable repository's entity", refs)
	}
	if slices.Contains(refs, "service:home/secret") {
		t.Errorf("refs = %v, leaked an entity from a repository the viewer cannot read", refs)
	}
}

// ADR-0012: ingester-emitted entities have no backing repository and therefore
// no natural access control, so showing them is a decision rather than a
// default. Silent over-sharing is the worse failure.
func TestObservedEntitiesAreHiddenUnlessAskedFor(t *testing.T) {
	db := newDB(t)
	seedTwoRepositories(t, db)

	hidden, err := db.VisibleTo(t.Context(), mainRef, index.Visibility{
		Repositories: []string{"example/public"},
	})
	if err != nil {
		t.Fatalf("VisibleTo: %v", err)
	}
	if slices.Contains(hidden, "service:cluster/seen") {
		t.Error("an observed entity was shown to a restricted viewer by default")
	}

	shown, err := db.VisibleTo(t.Context(), mainRef, index.Visibility{
		Repositories: []string{"example/public"}, Observed: true,
	})
	if err != nil {
		t.Fatalf("VisibleTo: %v", err)
	}
	if !slices.Contains(shown, "service:cluster/seen") {
		t.Error("an observed entity stayed hidden when it was explicitly allowed")
	}
}

// The single-operator posture, and the one every deployment starts in.
func TestUnrestrictedSeesEverything(t *testing.T) {
	db := newDB(t)
	seedTwoRepositories(t, db)

	refs, err := db.VisibleTo(t.Context(), mainRef, index.Unrestricted())
	if err != nil {
		t.Fatalf("VisibleTo: %v", err)
	}
	if len(refs) != 3 {
		t.Errorf("refs = %v, want all three", refs)
	}
}

// Somebody who can read nothing sees nothing. An empty set must not degrade
// into an unrestricted query, which is the classic way this goes wrong.
func TestAViewerWithNoAccessSeesNothing(t *testing.T) {
	db := newDB(t)
	seedTwoRepositories(t, db)

	refs, err := db.VisibleTo(t.Context(), mainRef, index.Visibility{Repositories: []string{}})
	if err != nil {
		t.Fatalf("VisibleTo: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("refs = %v, want nothing", refs)
	}
}
