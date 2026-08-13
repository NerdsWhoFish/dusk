package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
)

// Downloader fetches a whole tree at a commit.
type Downloader interface {
	Resolve(ctx context.Context, gitRef string) (string, error)
	ReadFileContents(ctx context.Context, ref, filePath string) (*githubapp.FileContents, error)
	Tarball(ctx context.Context, commit string, limits githubapp.Limits) (*catalogfs.Tree, error)
}

// ErrNotParticipating reports a repository with no root dusk.md. It is not a
// failure: the repository simply has not opted in, and nothing is downloaded.
var ErrNotParticipating = fmt.Errorf("repository has no %s", RootFile)

// Tarball is a Source that reads a whole tree in one request. Per ADR-0032, a
// call per file stopped being affordable once notes, `.dusk/` and documentation
// trees meant a repository contributes many files rather than one.
type Tarball struct {
	Repo   Downloader
	Limits githubapp.Limits

	mu       sync.Mutex
	tree     *catalogfs.Tree
	resolved map[string]string
}

// Resolve returns the commit a ref points at, and remembers it. The caller
// resolves to decide whether to reconcile at all and the loader resolves again
// to pin its reads, so without this one ref would cost two calls.
func (t *Tarball) Resolve(ctx context.Context, gitRef string) (string, error) {
	t.mu.Lock()
	if commit, ok := t.resolved[gitRef]; ok {
		t.mu.Unlock()
		return commit, nil
	}
	t.mu.Unlock()

	commit, err := t.Repo.Resolve(ctx, gitRef)
	if err != nil {
		return "", err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.resolved == nil {
		t.resolved = map[string]string{}
	}
	t.resolved[gitRef] = commit
	return commit, nil
}

// Tree downloads the repository, but only after a one-call probe confirms it
// opted in, which saves transferring the majority that hold nothing for Dusk.
// It is memoized so the controller and the loader share one download.
func (t *Tarball) Tree(ctx context.Context, commit string) (*catalogfs.Tree, error) {
	t.mu.Lock()
	cached := t.tree
	t.mu.Unlock()
	if cached != nil && cached.Commit() == commit {
		return cached, nil
	}

	if _, err := t.Repo.ReadFileContents(ctx, commit, RootFile); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotParticipating
		}
		return nil, err
	}

	limits := t.Limits
	if limits.MaxFiles == 0 {
		limits = githubapp.DefaultLimits
	}

	tree, err := t.Repo.Tarball(ctx, commit, limits)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.tree = tree
	return tree, nil
}
