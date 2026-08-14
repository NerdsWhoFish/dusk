package index_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

// readable is a repository the restricted viewer in these tests can read, and
// unreadable is one they cannot.
const (
	readable   = "example/public"
	unreadable = "example/private"
)

// onlyReadable is the viewer the leak tests are written against.
var onlyReadable = index.Visibility{Repositories: []string{readable}}

func seedTwoRepositories(t *testing.T, db *index.DB) {
	t.Helper()
	mustPut(t, db, readable, mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/open", "Open", "")}, nil)
	mustPut(t, db, unreadable, mainRef,
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
		Repositories: []string{readable},
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
		Repositories: []string{readable},
	})
	if err != nil {
		t.Fatalf("VisibleTo: %v", err)
	}
	if slices.Contains(hidden, "service:cluster/seen") {
		t.Error("an observed entity was shown to a restricted viewer by default")
	}

	shown, err := db.VisibleTo(t.Context(), mainRef, index.Visibility{
		Repositories: []string{readable}, Observed: true,
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

// seedForFilteredReads puts something on both sides of the line for every
// filtered read: a repository the viewer reads, one it cannot, and the
// observations drift compares a declaration against.
func seedForFilteredReads(t *testing.T, db *index.DB) {
	t.Helper()

	mustPut(t, db, readable, mainRef, []*duskv1alpha1.Entity{
		entity("service:home/open", "Open", ""),
		entity("service:home/retired", "Retired", ""),
	}, nil)
	mustPut(t, db, unreadable, mainRef, []*duskv1alpha1.Entity{
		entity("service:home/secret", "Secret", ""),
		entity("datastore:home/vault", "Vault", ""),
	}, nil)

	observe(t, db, "kubernetes", entity("service:home/open", "open", ""))
}

// ADR-0048: a count is of what the viewer can see. A tally of the whole estate
// would say how much is hidden, and per kind it would say what shape it is.
func TestADR0048_ACountIsOfWhatTheViewerCanSee(t *testing.T) {
	db := newDB(t)
	seedForFilteredReads(t, db)

	for _, test := range []struct {
		name string
		who  index.Visibility
		want map[string]int
	}{
		{
			name: "an operator counts the estate",
			who:  index.Unrestricted(),
			want: map[string]int{"service": 4, "datastore": 1},
		},
		{
			// datastore is absent rather than zero: a kind nobody can read is
			// a kind they have not heard of.
			name: "a restricted viewer counts their half",
			who:  onlyReadable,
			want: map[string]int{"service": 2},
		},
		{
			name: "observations join the count once they are allowed",
			who:  index.Visibility{Repositories: []string{readable}, Observed: true},
			want: map[string]int{"service": 3},
		},
		{
			name: "somebody who reads nothing counts nothing",
			who:  index.Visibility{Repositories: []string{}},
			want: map[string]int{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			counts, err := db.Kinds(t.Context(), mainRef, test.who)
			if err != nil {
				t.Fatalf("Kinds: %v", err)
			}

			got := map[string]int{}
			for _, count := range counts {
				got[count.Kind] = count.Count
			}
			if !maps.Equal(got, test.want) {
				t.Errorf("counts = %v, want %v", got, test.want)
			}
		})
	}
}

// ADR-0048: drift names refs, so it answers about the viewer's half of the
// catalog. Naming one from a repository they cannot open is the leak.
func TestADR0048_DriftNamesNoRefTheViewerCannotRead(t *testing.T) {
	db := newDB(t)
	seedForFilteredReads(t, db)

	operator := driftRefs(t, db, index.Unrestricted())
	for _, ref := range []string{"service:home/retired", "service:home/secret"} {
		if !slices.Contains(operator, ref) {
			t.Fatalf("drift = %v, want the operator to see %s", operator, ref)
		}
	}

	viewer := driftRefs(t, db, index.Visibility{
		Repositories: []string{readable}, Observed: true,
	})
	if slices.Contains(viewer, "service:home/secret") {
		t.Errorf("drift = %v, leaked a ref from a repository the viewer cannot read", viewer)
	}
	if !slices.Contains(viewer, "service:home/retired") {
		t.Errorf("drift = %v, want the viewer's own half still reported", viewer)
	}
}

// The default posture. Nothing observed is visible, so nothing is watched, and
// ADR-0038 keeps drift silent where nothing watches. The report is empty rather
// than every declaration reading as missing.
func TestDriftIsSilentWhenNoObservationIsVisible(t *testing.T) {
	db := newDB(t)
	seedForFilteredReads(t, db)

	if refs := driftRefs(t, db, onlyReadable); len(refs) != 0 {
		t.Errorf("drift = %v, want silence when the viewer can see no observation", refs)
	}
}

// A note lives in a repository like anything else, and its path is as much of
// a leak as an entity's ref.
func TestDriftDoesNotNameANoteInAnUnreadableRepository(t *testing.T) {
	db := newDB(t)
	err := db.Put(t.Context(), unreadable, mainRef, nil, nil, []*duskv1alpha1.Note{{
		Id: ".dusk/secret-plan.md", Kind: "gotcha", Refs: []string{"service:home/gone"},
	}})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if refs := driftRefs(t, db, index.Unrestricted()); !slices.Contains(refs, "service:home/gone") {
		t.Fatalf("drift = %v, want the operator to see the note pointing at nothing", refs)
	}
	if refs := driftRefs(t, db, onlyReadable); len(refs) != 0 {
		t.Errorf("drift = %v, leaked a note from a repository the viewer cannot read", refs)
	}
}

// ADR-0048: integrity names a ref and the files behind it, so both sides of the
// answer have to be computed over what the viewer can read.
func TestADR0048_IntegrityNamesNoFileTheViewerCannotRead(t *testing.T) {
	db := newDB(t)
	shared := entity("service:home/shared", "Shared", "")

	mustPut(t, db, readable, mainRef, []*duskv1alpha1.Entity{shared}, nil)
	mustPut(t, db, unreadable, mainRef, []*duskv1alpha1.Entity{shared},
		[]*duskv1alpha1.Relation{relation("service:home/shared", "host:home/typo", "runs_on")})

	operator := problemsFor(t, db, index.Unrestricted())
	if len(operator) != 2 {
		t.Fatalf("problems = %+v, want the duplicate and the dangling relation", operator)
	}

	// The duplicate is gone because the viewer can see one copy, and the
	// dangling relation because the relation itself is in the other repository.
	viewer := problemsFor(t, db, onlyReadable)
	if len(viewer) != 0 {
		t.Errorf("problems = %+v, want nothing about a repository the viewer cannot read", viewer)
	}
	for _, problem := range viewer {
		if strings.Contains(strings.Join(problem.Where, " "), unreadable) {
			t.Errorf("Where = %v, named a repository the viewer cannot read", problem.Where)
		}
	}
}

// ADR-0036 answers an invisible entity exactly as an absent one, and ADR-0048
// carries that into the reports. The cost is a dangling relation that is not
// really dangling; the alternative is an oracle for what exists elsewhere.
func TestADR0048_AnUnreadableTargetReadsAsAbsent(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, unreadable, mainRef,
		[]*duskv1alpha1.Entity{entity("host:home/nas", "NAS", "")}, nil)
	mustPut(t, db, readable, mainRef,
		[]*duskv1alpha1.Entity{entity("service:home/open", "Open", "")},
		[]*duskv1alpha1.Relation{relation("service:home/open", "host:home/nas", "runs_on")})

	if problems := problemsFor(t, db, index.Unrestricted()); len(problems) != 0 {
		t.Fatalf("problems = %+v, want a sound graph for the operator", problems)
	}

	viewer := problemsFor(t, db, onlyReadable)
	if len(viewer) != 1 || viewer[0].Ref != "host:home/nas" {
		t.Errorf("problems = %+v, want the unreadable target reported as dangling", viewer)
	}
}

// An orphaned scope names an ingester and says it observed something, which is
// the fact a viewer who may see no observation may not have.
func TestOrphansSayNothingToAViewerWhoCannotSeeObservations(t *testing.T) {
	db := newDB(t)
	observe(t, db, "kubernetes:prod", entity("host:prod/node-1", "node-1", ""))

	for _, test := range []struct {
		name string
		who  index.Visibility
		want int
	}{
		{"an operator", index.Unrestricted(), 1},
		{"a viewer allowed observations", index.Visibility{
			Repositories: []string{readable}, Observed: true,
		}, 1},
		{"a viewer denied observations", onlyReadable, 0},
		{"a viewer who reads nothing", index.Visibility{Repositories: []string{}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			problems, err := db.Orphans(t.Context(), nil, test.who)
			if err != nil {
				t.Fatalf("Orphans: %v", err)
			}
			if len(problems) != test.want {
				t.Errorf("orphans = %+v, want %d", problems, test.want)
			}
		})
	}
}

func driftRefs(t *testing.T, db *index.DB, v index.Visibility) []string {
	t.Helper()

	drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{}, v)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}

	refs := make([]string, 0, len(drifts))
	for _, drift := range drifts {
		refs = append(refs, drift.Ref)
	}
	return refs
}

func problemsFor(t *testing.T, db *index.DB, v index.Visibility) []index.Problem {
	t.Helper()

	problems, err := db.Integrity(t.Context(), mainRef, v)
	if err != nil {
		t.Fatalf("Integrity: %v", err)
	}
	return problems
}
