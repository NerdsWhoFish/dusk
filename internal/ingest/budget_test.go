package ingest_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/ingest"
)

// sharing is an ingester that draws on a named upstream system.
type sharing struct {
	name   string
	source string
	budget ingest.Budget

	mu   sync.Mutex
	runs int
	fail error
}

func (s *sharing) Name() string            { return s.name }
func (s *sharing) Interval() time.Duration { return time.Hour }
func (s *sharing) Source() string          { return s.source }
func (s *sharing) Budget() ingest.Budget   { return s.budget }

func (s *sharing) Observe(context.Context) (*ingest.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.runs++
	if s.fail != nil {
		return nil, s.fail
	}
	return &ingest.Observation{Entities: []*duskv1alpha1.Entity{{
		Ref: "thing:" + s.name + "/one", Kind: "thing", Namespace: s.name, Name: "one",
		Provenance: &duskv1alpha1.Provenance{Source: s.name},
	}}}, nil
}

func (s *sharing) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs
}

// ADR-0011: every ingester drawing on one source draws from one pool, rather
// than each assuming it has the whole quota.
func TestADR0011_IngestersOnOneSourceShareItsBudget(t *testing.T) {
	clock := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	budget := ingest.Budget{Concurrent: 1, Spacing: 5 * time.Minute}
	first := &sharing{name: "first", source: "one-api", budget: budget}
	second := &sharing{name: "second", source: "one-api", budget: budget}
	elsewhere := &sharing{name: "elsewhere", source: "another-api", budget: budget}

	scheduler := ingest.NewScheduler(newIndex(t), slog.New(slog.DiscardHandler), now, first, second, elsewhere)
	scheduler.RunDue(t.Context())

	if first.count()+second.count() != 1 {
		t.Fatalf("two ingesters on one source should not both run at once, got %d and %d",
			first.count(), second.count())
	}
	if elsewhere.count() != 1 {
		t.Fatalf("an ingester on another source should not be held up, it ran %d times", elsewhere.count())
	}

	// Still inside the spacing, so the deferred one waits rather than running.
	clock = clock.Add(time.Minute)
	scheduler.RunDue(t.Context())
	if first.count()+second.count() != 1 {
		t.Fatalf("the deferred run should still be waiting, got %d and %d", first.count(), second.count())
	}

	clock = clock.Add(5 * time.Minute)
	scheduler.RunDue(t.Context())
	if first.count()+second.count() != 2 {
		t.Fatalf("once the spacing has passed the deferred run should happen, got %d and %d",
			first.count(), second.count())
	}
}

// A deferral counted as a failure would let a busy source back its own
// ingesters off and eventually trip the breaker meant for a broken one.
func TestADR0011_BeingDeferredIsNotAFailure(t *testing.T) {
	clock := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	budget := ingest.Budget{Concurrent: 1, Spacing: time.Minute}
	first := &sharing{name: "first", source: "one-api", budget: budget}
	second := &sharing{name: "second", source: "one-api", budget: budget}

	scheduler := ingest.NewScheduler(newIndex(t), slog.New(slog.DiscardHandler), now, first, second)
	scheduler.RunDue(t.Context())

	for _, result := range scheduler.Status() {
		if result.Err != nil {
			t.Fatalf("%s reported a failure for what was only a deferral: %v", result.Ingester, result.Err)
		}
	}
	if len(scheduler.Status()) != 1 {
		t.Fatalf("a deferred run should record nothing at all, got %d results", len(scheduler.Status()))
	}
}

func TestAnIngesterWithNoSourceIsNotHeldUp(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC) }

	first := &sharing{name: "first", budget: ingest.Budget{Concurrent: 1, Spacing: time.Hour}}
	second := &sharing{name: "second", budget: ingest.Budget{Concurrent: 1, Spacing: time.Hour}}

	scheduler := ingest.NewScheduler(newIndex(t), slog.New(slog.DiscardHandler), now, first, second)
	scheduler.RunDue(t.Context())

	if first.count() != 1 || second.count() != 1 {
		t.Fatalf("an ingester that names no source shares nothing, got %d and %d",
			first.count(), second.count())
	}
}
