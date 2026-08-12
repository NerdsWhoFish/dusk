package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sync"

	"github.com/FetchHQ/dusk/pkg/githubapp"
)

// Downloader fetches a whole tree at a commit.
type Downloader interface {
	Resolve(ctx context.Context, gitRef string) (string, error)
	ReadFileContents(ctx context.Context, ref, filePath string) (*githubapp.FileContents, error)
	Tarball(ctx context.Context, commit string, limits githubapp.Limits) (*githubapp.Tree, error)
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

	mu   sync.Mutex
	tree *githubapp.Tree
}

// Resolve returns the commit a ref points at, which is also the cheapest way to
// learn that nothing has changed.
func (t *Tarball) Resolve(ctx context.Context, gitRef string) (string, error) {
	return t.Repo.Resolve(ctx, gitRef)
}

// Prepare downloads the tree, but only after confirming the repository opted
// in. The probe costs one call and saves transferring a repository with nothing
// in it for Dusk, which is the overwhelming majority of them.
func (t *Tarball) Prepare(ctx context.Context, commit string) error {
	if _, err := t.Repo.ReadFileContents(ctx, commit, RootFile); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotParticipating
		}
		return err
	}

	limits := t.Limits
	if limits.MaxFiles == 0 {
		limits = githubapp.DefaultLimits
	}

	tree, err := t.Repo.Tarball(ctx, commit, limits)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.tree = tree
	return nil
}

// ReadFile serves from the downloaded tree, so it costs nothing.
func (t *Tarball) ReadFile(_ context.Context, commit, filePath string) ([]byte, error) {
	tree, err := t.at(commit)
	if err != nil {
		return nil, err
	}
	data, ok := tree.File(filePath)
	if !ok {
		return nil, fmt.Errorf("reconcile: %q at %s: %w", filePath, short(commit), fs.ErrNotExist)
	}
	return data, nil
}

// Glob matches against the downloaded tree, so `**` is a walk rather than a
// pattern against a paginated listing.
func (t *Tarball) Glob(_ context.Context, commit, pattern string) ([]string, error) {
	tree, err := t.at(commit)
	if err != nil {
		return nil, err
	}

	var matches []string
	for _, candidate := range tree.Files() {
		ok, err := match(pattern, candidate)
		if err != nil {
			return nil, fmt.Errorf("reconcile: include pattern %q is malformed: %w", pattern, err)
		}
		if ok {
			matches = append(matches, candidate)
		}
	}
	return matches, nil
}

func (t *Tarball) at(commit string) (*githubapp.Tree, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.tree == nil {
		return nil, fmt.Errorf("reconcile: nothing downloaded, call Prepare first")
	}
	if t.tree.Commit != commit {
		return nil, fmt.Errorf("reconcile: the downloaded tree is %s, not %s", short(t.tree.Commit), short(commit))
	}
	return t.tree, nil
}

// match supports `**` for any number of directories, which path.Match does not
// and a documentation tree needs.
func match(pattern, candidate string) (bool, error) {
	before, after, recursive := splitRecursive(pattern)
	if !recursive {
		return path.Match(pattern, candidate)
	}

	// `a/**/b.md` also matches `a/b.md`, because requiring an intermediate
	// directory surprises everybody who writes it.
	for _, collapsed := range []string{before + "/" + after, before + "/*/" + after} {
		if ok, err := path.Match(collapsed, candidate); err != nil || ok {
			return ok, err
		}
	}
	if !hasPrefixDir(candidate, before) {
		return false, nil
	}
	return path.Match(after, path.Base(candidate))
}

func splitRecursive(pattern string) (before, after string, ok bool) {
	for i := range len(pattern) - 2 {
		if pattern[i:i+2] == "**" {
			return path.Clean(pattern[:max(i-1, 0)]), pattern[min(i+3, len(pattern)):], true
		}
	}
	return "", "", false
}

func hasPrefixDir(candidate, dir string) bool {
	if dir == "" || dir == "." {
		return true
	}
	return len(candidate) > len(dir) && candidate[:len(dir)] == dir && candidate[len(dir)] == '/'
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
