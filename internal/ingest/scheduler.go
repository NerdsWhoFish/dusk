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
	sources map[string]*source
	wake    chan struct{}
}

type state struct {
	ingester Ingester
	failures int
	next     time.Time
}

// source is one upstream system's shared budget, keyed by whatever the
// ingesters drawing on it agree to call it.
type source struct {
	inFlight int
	last     time.Time
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
	return &Scheduler{store: store, log: log, now: now, states: states, results: map[string]Result{}, wake: make(chan struct{}, 1)}
}

// Add puts an ingester into the rotation, due immediately. Installing a plugin
// has to start observing without a restart, or every install is a rollout.
func (s *Scheduler) Add(ingester Ingester) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[ingester.Name()] = &state{ingester: ingester}
	s.notify()
}

// Remove takes an ingester out. Its observations stay in the index, so
// uninstalling a plugin is not a way to delete catalog history.
func (s *Scheduler) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, name)
}

// Due brings an ingester's next run forward. An action that mutates its source
// calls it, because the catalog would otherwise serve a view from before the
// change until the interval came round.
func (s *Scheduler) Due(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if state, ok := s.states[name]; ok {
		state.next = time.Time{}
		s.notify()
	}
}

func (s *Scheduler) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Names lists the ingesters in the rotation, which is what makes an
// observation scope nobody claims identifiable as left behind.
func (s *Scheduler) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.states))
	for name := range s.states {
		names = append(names, name)
	}
	return names
}

// Status is what each ingester last did, and what the rotation intends to do
// next. The two travel together because a failed run is only readable against
// how many preceded it and when the next one is due.
func (s *Scheduler) Status() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Result, 0, len(s.results))
	for name, result := range s.results {
		if state, scheduled := s.states[name]; scheduled {
			result.Failures, result.Next = state.failures, state.next
		}
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
		case <-s.wake:
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

	release, admitted := s.admit(current)
	if !admitted {
		return
	}
	defer release()

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

// admit asks the shared budget for a turn. A refusal defers the run rather than
// failing it: a deferral counted as a failure would let a busy source trip the
// circuit breaker that exists for a broken one.
func (s *Scheduler) admit(current *state) (func(), bool) {
	sourced, shares := current.ingester.(Sourced)
	if !shares {
		return func() {}, true
	}

	key, budget := sourced.Source(), sourced.Budget()
	if key == "" {
		return func() {}, true
	}
	if budget.Concurrent < 1 {
		budget.Concurrent = 1
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sources == nil {
		s.sources = map[string]*source{}
	}
	shared, ok := s.sources[key]
	if !ok {
		shared = &source{}
		s.sources[key] = shared
	}

	now := s.now()
	waiting := budget.Spacing - now.Sub(shared.last)
	switch {
	case shared.inFlight >= budget.Concurrent:
		current.next = now.Add(budget.Spacing)
		return nil, false
	case !shared.last.IsZero() && waiting > 0:
		current.next = now.Add(waiting)
		return nil, false
	}

	shared.inFlight++
	shared.last = now

	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		shared.inFlight--
	}, true
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
