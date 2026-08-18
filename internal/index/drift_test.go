package index_test

import (
	"slices"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

func TestDecommissionedDeclarationIsNotReportedMissing(t *testing.T) {
	db := newDB(t)
	retired := entity("service:home/retired", "Retired", "")
	retired.Attributes, _ = structpb.NewStruct(map[string]any{"lifecycle": "decommissioned"})
	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{retired}, nil)
	observe(t, db, "kubernetes", entity("service:home/running", "Running", ""))

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if slices.ContainsFunc(drifts, func(d index.Drift) bool { return d.Ref == retired.GetRef() }) {
		t.Errorf("decommissioned declaration reported missing: %+v", drifts)
	}
}

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

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
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

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
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

// An ingester covers the namespace it observes in, not every namespace
// sharing a kind. ADR-0056's load-bearing rule, and the case it was written
// for: one cluster's ingester making a host nothing watches read as gone.
func TestADR0056_CoverageIsPerNamespaceNotPerKind(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("host:estate/nas", "The NAS", ""),
		entity("host:cluster-a/node-2", "Node 2", ""),
	}, nil)
	observe(t, db, "kubernetes", entity("host:cluster-a/node-1", "node-1", ""))

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}

	for _, drift := range drifts {
		if drift.Ref == "host:estate/nas" {
			t.Errorf("a declaration outside every observed namespace was reported: %+v", drift)
		}
	}

	// Inside the watched namespace absence is still evidence, or narrowing the
	// rule has silenced the report it exists to produce.
	if !slices.ContainsFunc(drifts, func(d index.Drift) bool {
		return d.Ref == "host:cluster-a/node-2" && d.Kind == index.DriftMissing
	}) {
		t.Errorf("a declaration inside the watched namespace was not reported: %+v", drifts)
	}
}

// Both directions still compare correctly when the undeclared half is asked
// for. ADR-0045 moved the default, not the comparison.
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

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{Undeclared: true}, index.Unrestricted())
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
		ObservedAs: []string{"service:prod/media-jellyfin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), testRepo, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	observe(t, db, "kubernetes", entity("service:prod/media-jellyfin", "media-jellyfin", ""))

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
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
		ObservedAs: []string{"service:prod/typo"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), testRepo, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	observe(t, db, "kubernetes", entity("service:prod/media-jellyfin", "media-jellyfin", ""))

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{Undeclared: true}, index.Unrestricted())
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(drifts) != 2 {
		t.Errorf("drift = %+v, want both sides reported when the alias matches nothing", drifts)
	}
}

// The shape that made the declared half one hundred percent false positives: an
// operator names one namespace for the estate, every plugin names what it read,
// and the two sets never intersect (ADR-0056).
func TestADR0056_AnEstateNothingObservesReportsNothing(t *testing.T) {
	declared := []*duskv1alpha1.Entity{
		entity("host:estate/nas", "The NAS", ""),
		entity("host:estate/router", "The router", ""),
		entity("service:estate/media", "Media", ""),
		entity("device:estate/switch", "The switch", ""),
		entity("network:estate/wifi", "Wi-Fi", ""),
	}
	observed := map[string][]*duskv1alpha1.Entity{
		"kubernetes": {
			entity("host:cluster-a/node-1", "node-1", ""),
			entity("service:cluster-a/ingress-ingress", "ingress", ""),
		},
		"appliance": {
			entity("device:appliance/00-11-22-33-44-55", "A phone", ""),
			entity("network:appliance/lan", "lan", ""),
		},
	}

	db := newDB(t)
	mustPut(t, db, testRepo, mainRef, declared, nil)
	for scope, entities := range observed {
		observe(t, db, scope, entities...)
	}

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	for _, drift := range drifts {
		if drift.Kind == index.DriftMissing {
			t.Errorf("an estate nothing observes reported a declaration as missing: %+v", drift)
		}
	}
}

// Writing an `observed_as` is the operator saying an ingester sees this, which
// is the witness the coverage rule looks for. Narrowing coverage to the
// observed namespace must not swallow a claim nothing bears out (ADR-0056).
func TestADR0056_AnObservedAsIsItsOwnWitness(t *testing.T) {
	db := newDB(t)
	err := db.Put(t.Context(), testRepo, mainRef, []index.Declaration{{
		Path:       "hosts/nas/dusk.md",
		Entity:     entity("host:estate/nas", "The NAS", ""),
		ObservedAs: []string{"host:containers/nas"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), testRepo, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
	observe(t, db, "kubernetes", entity("service:cluster-a/web", "web", ""))

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if !slices.ContainsFunc(drifts, func(d index.Drift) bool {
		return d.Ref == "host:estate/nas" && d.Kind == index.DriftMissing
	}) {
		t.Errorf("an observed_as no ingester bears out was not reported: %+v", drifts)
	}
}

// An observed entity is reality reporting itself, not a task, so drift stays
// quiet about it until asked. ADR-0045's load-bearing rule.
func TestADR0045_DriftIsSilentAboutWhatIsMerelyObserved(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, testRepo, mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")}, nil)
	observe(t, db, "kubernetes",
		entity("service:home/jellyfin", "jellyfin", ""),
		entity("service:home/surprise", "surprise", ""),
	)

	quiet, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(quiet) != 0 {
		t.Errorf("drift reported %d items by default, want silence about what is only observed: %+v", len(quiet), quiet)
	}

	asked, err := db.Drift(t.Context(), mainRef, index.DriftFilter{Undeclared: true}, index.Unrestricted())
	if err != nil {
		t.Fatalf("Drift asking for undeclared: %v", err)
	}
	if !slices.ContainsFunc(asked, func(d index.Drift) bool {
		return d.Ref == "service:home/surprise" && d.Kind == index.DriftUndeclared
	}) {
		t.Errorf("asking for the undeclared half did not return it: %+v", asked)
	}
}

// A note outlives what it was about. When the declaration goes, the note is
// still there pointing at nothing, and drift is where that surfaces.
func TestDriftReportsANoteWhoseSubjectIsGone(t *testing.T) {
	db := newDB(t)
	notes := []*duskv1alpha1.Note{{
		Id:   "notes/jellyfin-transcoding.md",
		Kind: "gotcha",
		Refs: []string{"service:home/jellyfin"},
	}}
	if err := db.Put(t.Context(), testRepo, mainRef, nil, nil, notes); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), testRepo, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}

	if !slices.ContainsFunc(drifts, func(d index.Drift) bool {
		return d.Ref == "service:home/jellyfin" && d.Kind == index.DriftNoteRef
	}) {
		t.Fatalf("a note pointing at nothing was not reported: %+v", drifts)
	}
}

// Drift tells you to close an orphaned note with status done or dropped, so
// doing it has to clear the row. Advice a report ignores leaves a queue that
// can never be emptied, which is what makes it noise (ADR-0056).
func TestADR0056_ClosingAnOrphanedNoteClearsIt(t *testing.T) {
	for _, status := range []string{"done", "dropped"} {
		t.Run(status, func(t *testing.T) {
			db := newDB(t)
			notes := []*duskv1alpha1.Note{{
				Id:     "notes/gone.md",
				Kind:   "idea",
				Status: status,
				Refs:   []string{"service:home/jellyfin"},
			}}
			if err := db.Put(t.Context(), testRepo, mainRef, nil, nil, notes); err != nil {
				t.Fatalf("Put: %v", err)
			}
			if err := db.SetDefaultView(t.Context(), testRepo, mainRef); err != nil {
				t.Fatalf("SetDefaultView: %v", err)
			}

			drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
			if err != nil {
				t.Fatalf("Drift: %v", err)
			}
			for _, drift := range drifts {
				if drift.Kind == index.DriftNoteRef {
					t.Errorf("a note closed as %q was still reported: %+v", status, drift)
				}
			}
		})
	}
}

// ADR-0031 accepted that a note's refs go unchecked at write time. Only the
// ref that resolves to nothing is reported, not the whole note.
func TestDriftReportsOnlyTheNoteRefThatResolvesToNothing(t *testing.T) {
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
	if err := db.SetDefaultView(t.Context(), testRepo, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	var reported []index.Drift
	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	for _, drift := range drifts {
		if drift.Kind == index.DriftNoteRef {
			reported = append(reported, drift)
		}
	}

	if len(reported) != 1 {
		t.Fatalf("found %d note refs pointing at nothing, want 1: %+v", len(reported), reported)
	}
	if reported[0].Ref != "service:home/jellifyn" {
		t.Errorf("ref = %q, want the typo", reported[0].Ref)
	}
	if !strings.Contains(reported[0].Declared, ".dusk/a.md") {
		t.Errorf("declared = %q, want the note holding the typo", reported[0].Declared)
	}
}

// Two ingesters seeing the same thing is agreement, not drift.
func TestDriftDoesNotReportTwoIngestersAgreeing(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, testRepo, mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")}, nil)

	observe(t, db, "kubernetes", entity("service:home/jellyfin", "jellyfin", ""))
	observe(t, db, "docker", entity("service:home/jellyfin", "jellyfin", ""))

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, index.Unrestricted())
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(drifts) != 0 {
		t.Errorf("agreement was reported as drift: %+v", drifts)
	}
}
