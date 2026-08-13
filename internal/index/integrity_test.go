package index_test

import (
	"slices"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

// A sound catalog reports nothing, or the signal is worthless.
func TestIntegrityIsQuietWhenTheGraphIsSound(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, testRepo, mainRef,
		[]*duskv1alpha1.Entity{
			entity("service:home/jellyfin", "Jellyfin", ""),
			entity("host:home/nas", "NAS", ""),
		},
		[]*duskv1alpha1.Relation{relation("service:home/jellyfin", "host:home/nas", "runs_on")},
	)

	problems, err := db.Integrity(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Integrity: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("a sound graph reported %d problems: %+v", len(problems), problems)
	}
}

// Two repositories declaring one ref is the failure that reads as an answer:
// Get returns whichever sorts first and says nothing about the other.
func TestIntegrityFindsADuplicateDeclaration(t *testing.T) {
	db := newDB(t)
	jellyfin := entity("service:home/jellyfin", "Jellyfin", "")

	mustPut(t, db, "example/homelab", mainRef, []*duskv1alpha1.Entity{jellyfin}, nil)
	mustPut(t, db, "example/media", mainRef, []*duskv1alpha1.Entity{jellyfin}, nil)

	problems := integrityOf(t, db, index.ProblemDuplicate)
	if len(problems) != 1 {
		t.Fatalf("found %d duplicates, want 1: %+v", len(problems), problems)
	}
	if problems[0].Ref != "service:home/jellyfin" {
		t.Errorf("ref = %q", problems[0].Ref)
	}

	// The fix starts by opening both files, so both have to be named.
	joined := strings.Join(problems[0].Where, " ")
	for _, repo := range []string{"example/homelab", "example/media"} {
		if !strings.Contains(joined, repo) {
			t.Errorf("Where = %v, want it to name %s", problems[0].Where, repo)
		}
	}
}

// Writing down the thing an ingester found is the whole point, so the two
// landing on one ref has to read as agreement. Reporting it as a conflict
// punishes the operator for doing the right thing.
func TestIntegrityAcceptsADeclarationOverItsOwnObservation(t *testing.T) {
	db := newDB(t)
	node := entity("host:prod/node-1", "node-1", "")

	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{node}, nil)
	observe(t, db, "kubernetes", node)

	if problems := integrityOf(t, db, index.ProblemDuplicate); len(problems) != 0 {
		t.Errorf("declaring an observed entity reported %d problems: %+v", len(problems), problems)
	}
}

// Two ingesters landing on one ref has no tiebreak at all, so the silence
// above must not extend to it.
func TestIntegrityFindsOneRefObservedByTwoIngesters(t *testing.T) {
	db := newDB(t)
	node := entity("host:prod/node-1", "node-1", "")

	observe(t, db, "kubernetes", node)
	observe(t, db, "nomad", node)

	problems := integrityOf(t, db, index.ProblemDuplicate)
	if len(problems) != 1 {
		t.Fatalf("found %d duplicates, want 1: %+v", len(problems), problems)
	}
	if !strings.Contains(problems[0].Detail, "observed") {
		t.Errorf("Detail = %q, want it to say these were observed", problems[0].Detail)
	}
}

// Moving an in-tree ingester to a plugin renames its scope, and the old one
// keeps answering with entities nothing will ever refresh. That reads as every
// ref being declared twice, which is how this was found.
func TestOrphansFindsAScopeNobodyClaims(t *testing.T) {
	db := newDB(t)
	observe(t, db, "kubernetes:mini-2", entity("host:prod/node-1", "node-1", ""))
	observe(t, db, "plugin:kubernetes", entity("host:prod/node-1", "node-1", ""))

	problems, err := db.Orphans(t.Context(), []string{"plugin:kubernetes"})
	if err != nil {
		t.Fatalf("Orphans: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("found %d orphans, want the ingester that is gone: %+v", len(problems), problems)
	}
	if !strings.Contains(problems[0].Ref, "kubernetes:mini-2") {
		t.Errorf("Ref = %q, want the scope nothing claims", problems[0].Ref)
	}
}

// A running ingester's scope is not abandoned, however stale it looks.
func TestOrphansLeavesLiveIngestersAlone(t *testing.T) {
	db := newDB(t)
	observe(t, db, "plugin:kubernetes", entity("host:prod/node-1", "node-1", ""))

	problems, err := db.Orphans(t.Context(), []string{"plugin:kubernetes"})
	if err != nil {
		t.Fatalf("Orphans: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("reported a live ingester as abandoned: %+v", problems)
	}
}

// Forgetting is the one deletion Dusk performs, so it only ever touches an
// observation scope and never a repository somebody declared.
func TestForgetRefusesARepository(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, testRepo, mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")}, nil)

	if err := db.Forget(t.Context(), testRepo); err == nil {
		t.Error("forgot a repository, which is not an observation scope")
	}
}

// A relation to nothing makes the graph look connected when it is not.
func TestIntegrityFindsADanglingRelation(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, testRepo, mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")},
		[]*duskv1alpha1.Relation{
			relation("service:home/jellyfin", "host:home/typo", "runs_on"),
		},
	)

	problems := integrityOf(t, db, index.ProblemDanglingRelation)
	if len(problems) != 1 {
		t.Fatalf("found %d dangling relations, want 1: %+v", len(problems), problems)
	}
	if problems[0].Ref != "host:home/typo" {
		t.Errorf("ref = %q, want the missing target", problems[0].Ref)
	}
	if !slices.Contains(problems[0].Where, "service:home/jellyfin") {
		t.Errorf("Where = %v, want the entity that points at it", problems[0].Where)
	}
}

// ADR-0031 accepted that a note's refs are unchecked at write time. This is
// where that stops being silent.
func TestIntegrityFindsANoteAttachedToNothing(t *testing.T) {
	db := newDB(t)
	err := db.Put(t.Context(), testRepo, mainRef,
		declare([]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")}),
		nil,
		[]*duskv1alpha1.Note{{
			Id: ".dusk/a.md", Kind: "gotcha", Body: "x",
			Refs: []string{"service:home/jellyfin", "service:home/jellifyn"},
		}},
	)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	problems := integrityOf(t, db, index.ProblemDanglingNote)
	if len(problems) != 1 {
		t.Fatalf("found %d dangling note refs, want 1: %+v", len(problems), problems)
	}
	if problems[0].Ref != "service:home/jellifyn" {
		t.Errorf("ref = %q, want the typo", problems[0].Ref)
	}
	if !slices.Contains(problems[0].Where, ".dusk/a.md") {
		t.Errorf("Where = %v, want the note holding the typo", problems[0].Where)
	}
}

// An entity declared in one repository and referenced from another is normal
// and correct. Reporting it would make the signal noise.
func TestIntegrityAcceptsACrossRepositoryReference(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, "example/hosts", mainRef,
		[]*duskv1alpha1.Entity{entity("host:home/nas", "NAS", "")}, nil)
	mustPut(t, db, "example/homelab", mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")},
		[]*duskv1alpha1.Relation{relation("service:home/jellyfin", "host:home/nas", "runs_on")},
	)

	if problems := integrityOf(t, db, index.ProblemDanglingRelation); len(problems) != 0 {
		t.Errorf("a valid cross-repository relation was reported: %+v", problems)
	}
}

func integrityOf(t *testing.T, db *index.DB, kind string) []index.Problem {
	t.Helper()
	all, err := db.Integrity(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Integrity: %v", err)
	}

	var matching []index.Problem
	for _, problem := range all {
		if problem.Kind == kind {
			matching = append(matching, problem)
		}
	}
	return matching
}
