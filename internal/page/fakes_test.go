package page_test

import (
	"context"
	"errors"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/FetchHQ/dusk/internal/index"
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

func (failing) RecentNotes(context.Context, string, int) ([]*duskv1alpha1.Note, error) {
	return nil, errBroken
}

func (failing) Drift(context.Context, string) ([]index.Drift, error) { return nil, errBroken }

func (failing) Integrity(context.Context, string) ([]index.Problem, error) { return nil, errBroken }

func (failing) Kinds(context.Context, string) ([]index.KindCount, error) { return nil, errBroken }

func (failing) Scopes(context.Context) ([]index.Scope, error) { return nil, errBroken }

// recording captures what a block actually asked the catalog.
type recording struct{}

var recorded struct{ searched, listedKind string }

func (*recording) Search(_ context.Context, _, query string, _ int) ([]index.SearchResult, error) {
	recorded.searched = query
	return nil, nil
}

func (*recording) List(_ context.Context, _, kind string) ([]*duskv1alpha1.Entity, error) {
	recorded.listedKind = kind
	return nil, nil
}

func (*recording) Get(context.Context, string, string) (*duskv1alpha1.Entity, error) {
	return nil, errBroken
}

func (*recording) RecentNotes(context.Context, string, int) ([]*duskv1alpha1.Note, error) {
	return nil, nil
}

func (*recording) Drift(context.Context, string) ([]index.Drift, error) { return nil, nil }

func (*recording) Integrity(context.Context, string) ([]index.Problem, error) { return nil, nil }

func (*recording) Kinds(context.Context, string) ([]index.KindCount, error) { return nil, nil }

func (*recording) Scopes(context.Context) ([]index.Scope, error) { return nil, nil }
