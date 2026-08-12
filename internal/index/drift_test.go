package index_test

import (
	"slices"
	"testing"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/FetchHQ/dusk/internal/index"
)

// observe stores entities the way an ingester does, so drift has two sides.
func observe(t *testing.T, db *index.DB, name string, entities ...*duskv1alpha1.Entity) {
	t.Helper()
	scope := index.ObservedScope(name)
	if err := db.Put(t.Context(), scope, mainRef, declare(entities), nil, nil); err != nil {
		t.Fatalf("Put observed: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), scope, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}

// With nothing observed, every entity is declared and unobserved. Reporting
// that as drift would name the whole catalog and mean nothing.
func TestDriftIsSilentWithNoIngesters(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, testRepo, mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")}, nil)

	drifts, err := db.Drift(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(drifts) != 0 {
		t.Errorf("drift with no ingesters reported %d items: %+v", len(drifts), drifts)
	}
}

// Nothing observes a repository, so a declared one is not missing, it is
// unwatched. Reporting it makes every kind an ingester does not cover into
// permanent drift nobody can ever clear.
func TestADR0038_DriftIsSilentWhereNothingObserves(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("repository:home/infra", "Infra", ""),
		entity("service:home/retired", "Retired thing", ""),
	}, nil)
	observe(t, db, "kubernetes", entity("service:home/surprise", "surprise", ""))

	drifts, err := db.Drift(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}

	for _, drift := range drifts {
		if drift.Ref == "repository:home/infra" {
			t.Errorf("an unwatched kind was reported as drift: %+v", drift)
		}
	}

	// The observed kind still reports, or the filter has swallowed the signal.
	if !slices.ContainsFunc(drifts, func(d index.Drift) bool {
		return d.Ref == "service:home/retired" && d.Kind == index.DriftMissing
	}) {
		t.Errorf("a declared service nothing observed was not reported: %+v", drifts)
	}
}

// The two questions worth asking of an estate: what did I write down that is
// gone, and what is running that nobody wrote down.
func TestDriftComparesDeclaredAgainstObserved(t *testing.T) {
	db := newDB(t)

	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", ""),
		entity("service:home/retired", "Retired thing", ""),
	}, nil)
	observe(t, db, "kubernetes",
		entity("service:home/jellyfin", "jellyfin", ""),
		entity("service:home/surprise", "surprise", ""),
	)

	drifts, err := db.Drift(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}

	byRef := map[string]index.Drift{}
	for _, drift := range drifts {
		byRef[drift.Ref] = drift
	}

	if _, ok := byRef["service:home/jellyfin"]; ok {
		t.Error("an entity both declared and observed was reported as drift")
	}

	missing, ok := byRef["service:home/retired"]
	if !ok {
		t.Fatalf("declared-but-gone was not reported: %+v", drifts)
	}
	if missing.Kind != index.DriftMissing {
		t.Errorf("kind = %q, want %q", missing.Kind, index.DriftMissing)
	}
	if missing.Declared != testRepo {
		t.Errorf("declared = %q, want the repository that declares it", missing.Declared)
	}

	undeclared, ok := byRef["service:home/surprise"]
	if !ok {
		t.Fatalf("observed-but-undeclared was not reported: %+v", drifts)
	}
	if undeclared.Kind != index.DriftUndeclared {
		t.Errorf("kind = %q, want %q", undeclared.Kind, index.DriftUndeclared)
	}
	if undeclared.Observed != index.ObservedScope("kubernetes") {
		t.Errorf("observed = %q, want the ingester that saw it", undeclared.Observed)
	}
}

// A human and an ingester never independently pick the same name. Without a
// mapping, every entity appears on both sides of the report and drift is
// noise rather than a signal.
func TestDriftFollowsObservedAsAliases(t *testing.T) {
	db := newDB(t)

	declared := entity("service:home/jellyfin", "Jellyfin", "")
	err := db.Put(t.Context(), testRepo, mainRef, []index.Declaration{{
		Path:       "services/jellyfin/dusk.md",
		Entity:     declared,
		ObservedAs: []string{"service:mini-2/media-jellyfin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), testRepo, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	observe(t, db, "kubernetes", entity("service:mini-2/media-jellyfin", "media-jellyfin", ""))

	drifts, err := db.Drift(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(drifts) != 0 {
		t.Errorf("a declared entity and its observed counterpart both reported as drift: %+v", drifts)
	}
}

// An alias naming something nothing observes is still drift: the mapping is a
// claim, and a claim that does not hold is exactly what should be reported.
func TestDriftStillReportsAnAliasThatMatchesNothing(t *testing.T) {
	db := newDB(t)

	err := db.Put(t.Context(), testRepo, mainRef, []index.Declaration{{
		Path:       "services/jellyfin/dusk.md",
		Entity:     entity("service:home/jellyfin", "Jellyfin", ""),
		ObservedAs: []string{"service:mini-2/typo"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), testRepo, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	observe(t, db, "kubernetes", entity("service:mini-2/media-jellyfin", "media-jellyfin", ""))

	drifts, err := db.Drift(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(drifts) != 2 {
		t.Errorf("drift = %+v, want both sides reported when the alias matches nothing", drifts)
	}
}

// Two ingesters seeing the same thing is agreement, not drift.
func TestDriftDoesNotReportTwoIngestersAgreeing(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, testRepo, mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")}, nil)

	observe(t, db, "kubernetes", entity("service:home/jellyfin", "jellyfin", ""))
	observe(t, db, "docker", entity("service:home/jellyfin", "jellyfin", ""))

	drifts, err := db.Drift(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(drifts) != 0 {
		t.Errorf("agreement was reported as drift: %+v", drifts)
	}
}
