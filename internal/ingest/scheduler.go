package ingest

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"
)

// Concurrency caps how many ingesters run at once, so a restart does not
// stampede every source system simultaneously (ADR-0011).
const Concurrency = 4

// MaxBackoff bounds the retry delay. Past this an ingester is failing for a
// reason waiting will not fix, and hammering it helps nobody.
const MaxBackoff = 30 * time.Minute

// BreakerThreshold is how many consecutive failures trip an ingester out of
// the rotation, so one broken source cannot spend the budget the others need.
const BreakerThreshold = 8

// Scheduler runs ingesters on their own intervals.
type Scheduler struct {
	store Store
	log   *slog.Logger
	now   func() time.Time

	// mu guards states as well as results, because installing a plugin adds an
	// ingester to a scheduler that is already running.
	mu      sync.Mutex
	states  map[string]*state
	results map[string]Result
}

type state struct {
	ingester Ingester
	failures int
	next     time.Time
}

// NewScheduler builds a scheduler over a fixed set of ingesters.
func NewScheduler(store Store, log *slog.Logger, now func() time.Time, ingesters ...Ingester) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}

	states := make(map[string]*state, len(ingesters))
	for _, ingester := range ingesters {
		states[ingester.Name()] = &state{ingester: ingester}
	}
	return &Scheduler{store: store, log: log, now: now, states: states, results: map[string]Result{}}
}

// Any reports whether there is anything to run, so a deployment with no
// ingesters configured does not start a loop that never does anything.
func (s *Scheduler) Any() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.states) > 0
}

// Add puts an ingester into the rotation, due immediately. Installing a plugin
// has to start observing without a restart, or every install is a rollout.
func (s *Scheduler) Add(ingester Ingester) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[ingester.Name()] = &state{ingester: ingester}
}

// Remove takes an ingester out. Its observations stay in the index, so
// uninstalling a plugin is not a way to delete catalog history.
func (s *Scheduler) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, name)
}

// Status is what each ingester last did.
func (s *Scheduler) Status() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Result, 0, len(s.results))
	for _, result := range s.results {
		out = append(out, result)
	}
	return out
}

// Start runs due ingesters until the context ends. It ticks far more often
// than any interval, because the tick only decides whether anything is due.
func (s *Scheduler) Start(ctx context.Context) {
	const tick = 30 * time.Second

	s.RunDue(ctx)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunDue(ctx)
		}
	}
}

// RunDue runs every ingester whose interval has elapsed, bounded by
// Concurrency. Exported so a test can drive it without a clock.
func (s *Scheduler) RunDue(ctx context.Context) {
	now := s.now()

	s.mu.Lock()
	var due []*state
	for _, candidate := range s.states {
		if !candidate.next.After(now) {
			due = append(due, candidate)
		}
	}
	s.mu.Unlock()

	if len(due) == 0 {
		return
	}

	slots := make(chan struct{}, Concurrency)
	var wait sync.WaitGroup

	for _, current := range due {
		wait.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			s.runOne(ctx, current)
		})
	}
	wait.Wait()
}

func (s *Scheduler) runOne(ctx context.Context, current *state) {
	name := current.ingester.Name()
	result := Run(ctx, current.ingester, s.store, s.now)

	s.mu.Lock()
	s.results[name] = result
	if result.Err != nil {
		current.failures++
		current.next = s.now().Add(backoff(current.failures, current.ingester.Interval()))
	} else {
		current.failures = 0
		current.next = s.now().Add(current.ingester.Interval())
	}
	failures, next := current.failures, current.next
	s.mu.Unlock()

	if result.Err != nil {
		// Kept as a warning rather than an error: the catalog is intact and
		// serving what it last saw, which is the designed behaviour.
		s.log.Warn("ingester failed, keeping what it last observed",
			"ingester", name, "consecutive_failures", failures,
			"next_attempt", next, "error", result.Err)

		if failures == BreakerThreshold {
			s.log.Error("ingester is failing persistently and is now backed off to the maximum",
				"ingester", name, "failures", failures)
		}
		return
	}

	s.log.Info("observed",
		"ingester", name, "entities", result.Entities, "relations", result.Relations)
}

// backoff grows exponentially from the ingester's own interval and stops at
// MaxBackoff. Past BreakerThreshold it stays there rather than growing without
// bound, which is the circuit breaker in ADR-0011.
func backoff(failures int, interval time.Duration) time.Duration {
	if failures >= BreakerThreshold {
		return MaxBackoff
	}
	if interval <= 0 {
		interval = time.Minute
	}

	grown := float64(interval) * math.Pow(2, float64(failures-1))
	if grown > float64(MaxBackoff) {
		return MaxBackoff
	}
	return time.Duration(grown)
}
