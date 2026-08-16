package ingest_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/ingest"
)

var observedAt = time.Date(2026, 8, 12, 4, 0, 0, 0, time.UTC)

// fake is an ingester a test can make succeed, fail, or change its mind.
type fake struct {
	name     string
	interval time.Duration
	entities []*duskv1alpha1.Entity
	err      error
	runs     int
}

func (f *fake) Name() string            { return f.name }
func (f *fake) Interval() time.Duration { return f.interval }

func (f *fake) Observe(context.Context) (*ingest.Observation, error) {
	f.runs++
	if f.err != nil {
		return nil, f.err
	}
	return &ingest.Observation{Entities: f.entities}, nil
}

func entity(ref string) *duskv1alpha1.Entity {
	return &duskv1alpha1.Entity{
		Ref: ref, Kind: "service", Namespace: "cluster", Name: ref,
	}
}

func newIndex(t *testing.T) *index.DB {
	t.Helper()
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func observedCount(t *testing.T, db *index.DB) int {
	t.Helper()
	entities, err := db.List(t.Context(), ingest.ObservedRef, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return len(entities)
}

// ADR-0011: a failing ingester never deletes. "I could not look" and "it is
// not there" must never be the same thing, and an implementation that clears
// the scope before it knows the run succeeded gets this exactly wrong.
func TestADR0011_FailedIngestDoesNotDelete(t *testing.T) {
	db := newIndex(t)
	source := &fake{
		name:     "kubernetes",
		interval: time.Minute,
		entities: []*duskv1alpha1.Entity{entity("a"), entity("b")},
	}

	if result := ingest.Run(t.Context(), source, db, clock()); result.Err != nil {
		t.Fatalf("first run: %v", result.Err)
	}
	if got := observedCount(t, db); got != 2 {
		t.Fatalf("observed %d entities after a good run, want 2", got)
	}

	// The cluster goes away.
	source.err = errors.New("dial tcp: connection refused")
	result := ingest.Run(t.Context(), source, db, clock())

	if result.Err == nil {
		t.Fatal("a failing ingester reported success")
	}
	if got := observedCount(t, db); got != 2 {
		t.Errorf("observed %d entities after a failed run, want the previous 2 kept", got)
	}

	t.Run("and a later success replaces them", func(t *testing.T) {
		source.err = nil
		source.entities = []*duskv1alpha1.Entity{entity("a")}

		if result := ingest.Run(t.Context(), source, db, clock()); result.Err != nil {
			t.Fatalf("recovery run: %v", result.Err)
		}
		if got := observedCount(t, db); got != 1 {
			t.Errorf("observed %d entities, want 1: a successful run is authoritative", got)
		}
	})
}

// An ingester that returns neither data nor an error is a bug in the
// ingester, and treating it as an empty source would delete the catalog.
func TestAnEmptyResultWithNoErrorIsRefused(t *testing.T) {
	db := newIndex(t)
	if err := seed(t, db); err != nil {
		t.Fatal(err)
	}

	result := ingest.Run(t.Context(), &silent{}, db, clock())
	if result.Err == nil {
		t.Fatal("a nil observation with no error was accepted")
	}
	if got := observedCount(t, db); got != 1 {
		t.Errorf("observed %d entities, want the previous 1 kept", got)
	}
}

type silent struct{}

func (silent) Name() string            { return "silent" }
func (silent) Interval() time.Duration { return time.Minute }
func (silent) Observe(context.Context) (*ingest.Observation, error) {
	return nil, nil
}

func seed(t *testing.T, db *index.DB) error {
	t.Helper()
	source := &fake{name: "silent", interval: time.Minute, entities: []*duskv1alpha1.Entity{entity("a")}}
	return ingest.Run(t.Context(), source, db, clock()).Err
}

// Observations are stored apart from declarations, which is what makes the
// two comparable rather than one silently overwriting the other.
func TestObservationsAreScopedAwayFromDeclarations(t *testing.T) {
	db := newIndex(t)
	source := &fake{name: "kubernetes", interval: time.Minute, entities: []*duskv1alpha1.Entity{entity("a")}}

	if result := ingest.Run(t.Context(), source, db, clock()); result.Err != nil {
		t.Fatalf("Run: %v", result.Err)
	}

	scopes, err := db.Scopes(t.Context())
	if err != nil {
		t.Fatalf("Scopes: %v", err)
	}
	if len(scopes) != 1 {
		t.Fatalf("scopes = %+v, want one", scopes)
	}
	if scopes[0].Repository != "ingester:kubernetes" {
		t.Errorf("repository = %q, want the reserved ingester scope", scopes[0].Repository)
	}
	if scopes[0].GitRef != ingest.ObservedRef {
		t.Errorf("ref = %q, want %q", scopes[0].GitRef, ingest.ObservedRef)
	}
	if !ingest.IsObserved(scopes[0].Repository) {
		t.Error("IsObserved did not recognise its own scope, so a UI would offer to clone it")
	}
}

// Backoff has to grow, or a source that is down gets hammered at its normal
// interval, and it has to stop growing, or recovery takes hours.
func TestSchedulerBacksOffAndRecovers(t *testing.T) {
	db := newIndex(t)
	source := &fake{name: "kubernetes", interval: time.Minute, err: errors.New("down")}

	at := observedAt
	scheduler := ingest.NewScheduler(db, slog.New(slog.DiscardHandler), func() time.Time { return at }, source)

	scheduler.RunDue(t.Context())
	if source.runs != 1 {
		t.Fatalf("ran %d times, want 1", source.runs)
	}

	// Still inside the backoff window, so nothing should run.
	at = at.Add(30 * time.Second)
	scheduler.RunDue(t.Context())
	if source.runs != 1 {
		t.Errorf("ran %d times, want 1: a failing ingester should not retry immediately", source.runs)
	}

	at = at.Add(10 * time.Minute)
	scheduler.RunDue(t.Context())
	if source.runs != 2 {
		t.Errorf("ran %d times, want 2: backoff should have elapsed", source.runs)
	}

	t.Run("status reports the failure rather than hiding it", func(t *testing.T) {
		status := scheduler.Status()
		if len(status) != 1 || status[0].Err == nil {
			t.Errorf("status = %+v, want one failing entry", status)
		}
	})

	// A failure is only readable against how many came before it and when the
	// next attempt is, and both were known to the rotation and to nobody else.
	t.Run("status reports how long it has been failing and when it retries", func(t *testing.T) {
		status := scheduler.Status()
		if got := status[0].Failures; got != 2 {
			t.Errorf("Failures = %d, want 2", got)
		}
		if !status[0].Next.After(at) {
			t.Errorf("Next = %v, want a retry after %v", status[0].Next, at)
		}
	})
}

func clock() func() time.Time { return func() time.Time { return observedAt } }

// failing is an ingester that can never look, which is how a plugin whose
// credentials are wrong behaves.
type failing struct {
	name  string
	cause error
}

func (f failing) Name() string            { return f.name }
func (f failing) Interval() time.Duration { return time.Hour }

func (f failing) Observe(context.Context) (*ingest.Observation, error) { return nil, f.cause }

// A source Dusk cannot even connect to must not stop the process. Refusing to
// start would take the whole catalog down over one bad credential, and the
// catalog is still correct about everything else.
func TestAnUnreachableSourceReportsRatherThanCrashing(t *testing.T) {
	db := newIndex(t)
	broken := failing{name: "cluster", cause: errors.New("no service account token")}

	result := ingest.Run(t.Context(), broken, db, clock())
	if result.Err == nil {
		t.Fatal("an unreachable source reported success")
	}
	if !strings.Contains(result.Err.Error(), "no service account token") {
		t.Errorf("err = %v, want the original cause", result.Err)
	}
	if result.Ingester != "cluster" {
		t.Errorf("ingester = %q, want it named so status can show it", result.Ingester)
	}

	t.Run("and it appears in scheduler status", func(t *testing.T) {
		at := observedAt
		scheduler := ingest.NewScheduler(db, slog.New(slog.DiscardHandler),
			func() time.Time { return at }, broken)
		scheduler.RunDue(t.Context())

		status := scheduler.Status()
		if len(status) != 1 || status[0].Err == nil {
			t.Errorf("status = %+v, want the failure visible", status)
		}
	})
}

// ADR-0034: a human who wrote something down beats an ingester that inferred
// it. Without an explicit order the winner is decided by how the scope names
// happen to sort, which is a coin toss that would flip on a rename.
func TestADR0034_DeclaredBeatsObserved(t *testing.T) {
	db := newIndex(t)

	declared := entity("postgres")
	declared.Title = "Postgres, as documented"
	if err := db.Put(t.Context(), "example/homelab", ingest.ObservedRef,
		[]index.Declaration{{Path: "dusk.md", Entity: declared}}, nil, nil); err != nil {
		t.Fatalf("Put declared: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), "example/homelab", ingest.ObservedRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	seen := entity("postgres")
	seen.Title = "postgres, as found in the cluster"
	source := &fake{name: "kubernetes", interval: time.Minute, entities: []*duskv1alpha1.Entity{seen}}
	if result := ingest.Run(t.Context(), source, db, clock()); result.Err != nil {
		t.Fatalf("Run: %v", result.Err)
	}

	got, err := db.Get(t.Context(), "", "postgres")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetTitle() != "Postgres, as documented" {
		t.Errorf("title = %q, want the declared one to win", got.GetTitle())
	}
}
