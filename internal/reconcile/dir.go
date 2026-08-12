package reconcile

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/FetchHQ/dusk/pkg/githubapp"
)

// Dir is a Source backed by a directory on disk. A directory has no refs, so it
// serves exactly one and rejects any other rather than quietly returning the
// same tree whatever it is asked for.
type Dir struct {
	root   *os.Root
	gitRef string
}

// NewDir opens dir as the tree for gitRef. Reads go through os.Root, so a path
// escaping the directory fails at the filesystem rather than relying on the
// caller having sanitised it.
func NewDir(dir, gitRef string) (*Dir, error) {
	if gitRef == "" {
		return nil, fmt.Errorf("reconcile: open %q: a git ref is required", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("reconcile: open %q: %w", dir, err)
	}
	return &Dir{root: root, gitRef: gitRef}, nil
}

// Close releases the directory handle.
func (d *Dir) Close() error {
	if err := d.root.Close(); err != nil {
		return fmt.Errorf("reconcile: close: %w", err)
	}
	return nil
}

// Resolve reports the ref this directory serves. A working tree has no commits,
// so there is nothing to resolve to and the ref stands in for one.
func (d *Dir) Resolve(_ context.Context, gitRef string) (string, error) {
	if err := d.check(gitRef); err != nil {
		return "", err
	}
	return gitRef, nil
}

// ReadFile returns the contents of filePath from within the directory.
func (d *Dir) ReadFile(_ context.Context, gitRef, filePath string) ([]byte, error) {
	if err := d.check(gitRef); err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(d.root.FS(), filePath)
	if err != nil {
		return nil, fmt.Errorf("reconcile: read %q: %w", filePath, err)
	}
	return data, nil
}

// Glob returns the markdown paths matching pattern, in lexical order. It walks
// rather than calling fs.Glob so `**`, `.git` and non-markdown behave as they
// do reading a tarball, since a local check that disagrees is worse than none.
func (d *Dir) Glob(_ context.Context, gitRef, pattern string) ([]string, error) {
	if err := d.check(gitRef); err != nil {
		return nil, err
	}

	var matches []string
	err := fs.WalkDir(d.root.FS(), ".", func(candidate string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case entry.IsDir():
			if candidate == ".git" {
				return fs.SkipDir
			}
			return nil
		case !githubapp.IsMarkdown(candidate):
			return nil
		}

		ok, err := match(pattern, candidate)
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, candidate)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile: glob %q: %w", pattern, err)
	}
	return matches, nil
}

func (d *Dir) check(gitRef string) error {
	if gitRef != d.gitRef {
		return fmt.Errorf("reconcile: this directory is the tree for %q, not %q", d.gitRef, gitRef)
	}
	return nil
}
