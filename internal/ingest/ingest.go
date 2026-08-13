// Package ingest observes infrastructure and puts what it finds in the index.
//
// It is the half of the catalog nobody types. A declaration is what somebody
// wrote in a repository; an observation is what an ingester found by looking,
// and the two are kept apart so that comparing them is possible.
package ingest

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

// ObservedRef is the git ref every observation is stored under. Observations
// have no commit, so this stands in for one and keeps them out of the way of
// anything read at a branch.
const ObservedRef = "refs/dusk/observed"

// Ingester looks at one source system and reports what is there.
type Ingester interface {
	// Name identifies the ingester, and becomes part of the scope its
	// observations are stored under. Stable across runs.
	Name() string

	// Interval is how often it should run.
	Interval() time.Duration

	// Observe returns everything the source system has. An error must mean
	// "I could not look", never "there is nothing there": that difference
	// decides whether the catalog keeps what it knew (ADR-0011).
	Observe(ctx context.Context) (*Observation, error)
}

// Budget is how much of one upstream system a set of ingesters may take between
// them, in runs rather than requests: what an ingester does upstream is inside
// its own process, so a request count is not a number this can hold itself to.
type Budget struct {
	// Concurrent runs allowed against one source. Zero or less means one.
	Concurrent int

	// Spacing is the least time between two runs against one source.
	Spacing time.Duration
}

// Sourced is an ingester that shares an upstream system with others. Two of
// them returning the same Source draw on one Budget, which is what stops each
// assuming it has the whole quota (ADR-0011).
type Sourced interface {
	Ingester

	Source() string
	Budget() Budget
}

// Observation is one run's complete view of its source. Complete by contract:
// anything previously observed and absent here is treated as gone, so a
// partial view has to be an error instead.
type Observation struct {
	Entities  []*duskv1alpha1.Entity
	Relations []*duskv1alpha1.Relation
}

// Scope is where an ingester's observations live. The index owns the shape,
// because it is what partitions and orders by it.
func Scope(name string) string { return index.ObservedScope(name) }

// IsObserved reports whether a repository slot holds observations rather than
// a real repository, so a caller can avoid offering to clone it.
func IsObserved(repository string) bool { return index.IsObserved(repository) }

// Store is the slice of the index an ingest run needs.
type Store interface {
	Put(ctx context.Context, repository, gitRef string, declarations []index.Declaration, relations []*duskv1alpha1.Relation, notes []*duskv1alpha1.Note) error
	DropRepository(ctx context.Context, repository, gitRef string) error

	// SetDefaultView puts a scope in reach of a query that names no ref.
	// Without it an observation is stored and never read.
	SetDefaultView(ctx context.Context, repository, gitRef string) error
}

// Result is what one run did, for the status feed.
type Result struct {
	Ingester  string
	Entities  int
	Relations int
	At        time.Time

	// Err is set when the run failed. The previous observation is still in
	// the index when it is: a failure is not an absence.
	Err error
}

// Run executes one ingester and stores what it found. A failed run stores
// nothing and removes nothing (ADR-0011), the rule most easily broken by
// replacing contents before knowing the run succeeded.
func Run(ctx context.Context, ingester Ingester, store Store, now func() time.Time) Result {
	name := ingester.Name()
	result := Result{Ingester: name, At: now()}

	observed, err := ingester.Observe(ctx)
	if err != nil {
		result.Err = fmt.Errorf("ingest: %s could not look: %w", name, err)
		return result
	}
	if observed == nil {
		result.Err = fmt.Errorf("ingest: %s returned nothing and no error, which cannot be distinguished from an empty source", name)
		return result
	}

	declarations := make([]index.Declaration, 0, len(observed.Entities))
	for _, entity := range observed.Entities {
		stamp(entity, name, result.At)
		declarations = append(declarations, index.Declaration{
			// Observations have no file. The path records what saw it, so a
			// reader is never told an entity came from a dusk.md that does
			// not exist.
			Path:   "observed by " + name,
			Entity: entity,
		})
	}
	for _, relation := range observed.Relations {
		stampRelation(relation, name, result.At)
	}

	// Only now, with a complete and successful observation in hand.
	if err := store.Put(ctx, Scope(name), ObservedRef, declarations, observed.Relations, nil); err != nil {
		result.Err = err
		return result
	}
	if err := store.SetDefaultView(ctx, Scope(name), ObservedRef); err != nil {
		result.Err = err
		return result
	}

	result.Entities = len(observed.Entities)
	result.Relations = len(observed.Relations)
	return result
}

// stamp records who saw an entity and when, so staleness is derived from the
// entity rather than tracked beside it (ADR-0011).
func stamp(entity *duskv1alpha1.Entity, name string, at time.Time) {
	if entity.GetProvenance() == nil {
		entity.Provenance = &duskv1alpha1.Provenance{}
	}
	entity.Provenance.Source = Scope(name)
	entity.Provenance.Version = ObservedRef
	entity.Provenance.ObservedAt = timestamp(at)
}

func stampRelation(relation *duskv1alpha1.Relation, name string, at time.Time) {
	if relation.GetProvenance() == nil {
		relation.Provenance = &duskv1alpha1.Provenance{}
	}
	relation.Provenance.Source = Scope(name)
	relation.Provenance.Version = ObservedRef
	relation.Provenance.ObservedAt = timestamp(at)
}

func timestamp(at time.Time) *timestamppb.Timestamp { return timestamppb.New(at) }
