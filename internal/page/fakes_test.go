package page_test

import (
	"context"
	"errors"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

// failing makes every query fail, so a test can assert that a errBroken block
// carries its reason rather than taking the page down.
type failing struct{}

var errBroken = errors.New("the index is unavailable")

func (failing) List(context.Context, string, string) ([]*duskv1alpha1.Entity, error) {
	return nil, errBroken
}

func (failing) Search(context.Context, string, string, int) ([]index.SearchResult, error) {
	return nil, errBroken
}

func (failing) Get(context.Context, string, string) (*duskv1alpha1.Entity, error) {
	return nil, errBroken
}

func (failing) Notes(context.Context, string, index.NoteFilter) ([]*duskv1alpha1.Note, error) {
	return nil, errBroken
}

func (failing) Drift(context.Context, string, index.DriftFilter) ([]index.Drift, error) {
	return nil, errBroken
}

func (failing) Integrity(context.Context, string) ([]index.Problem, error) { return nil, errBroken }

func (failing) Kinds(context.Context, string) ([]index.KindCount, error) { return nil, errBroken }

func (failing) Neighbors(context.Context, string, string) ([]*duskv1alpha1.Relation, error) {
	return nil, errBroken
}

func (failing) Scopes(context.Context) ([]index.Scope, error) { return nil, errBroken }

// recording captures what a block actually asked the catalog.
type recording struct{}

var recorded struct {
	searched, listedKind, neighborsOf string
	relations                         []*duskv1alpha1.Relation
	listed                            []*duskv1alpha1.Entity
}

func (*recording) Search(_ context.Context, _, query string, _ int) ([]index.SearchResult, error) {
	recorded.searched = query
	return nil, nil
}

func (*recording) List(_ context.Context, _, kind string) ([]*duskv1alpha1.Entity, error) {
	recorded.listedKind = kind
	return recorded.listed, nil
}

func (*recording) Get(context.Context, string, string) (*duskv1alpha1.Entity, error) {
	return nil, errBroken
}

func (*recording) Notes(context.Context, string, index.NoteFilter) ([]*duskv1alpha1.Note, error) {
	return nil, nil
}

func (*recording) Drift(context.Context, string, index.DriftFilter) ([]index.Drift, error) {
	return nil, nil
}

func (*recording) Integrity(context.Context, string) ([]index.Problem, error) { return nil, nil }

func (*recording) Kinds(context.Context, string) ([]index.KindCount, error) { return nil, nil }

func (*recording) Scopes(context.Context) ([]index.Scope, error) { return nil, nil }

// Neighbors backs a `related:` query. It returns whatever relations the test
// gave it, regardless of ref, because what is under test is the filtering.
func (*recording) Neighbors(_ context.Context, _, ref string) ([]*duskv1alpha1.Relation, error) {
	recorded.neighborsOf = ref
	return recorded.relations, nil
}
