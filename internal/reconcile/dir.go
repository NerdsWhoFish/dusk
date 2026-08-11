package reconcile

import (
	"context"
	"fmt"
	"io/fs"
	"os"
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

// Glob returns the paths matching pattern, in lexical order. Patterns are
// path.Match syntax, so `*` does not cross a separator and there is no `**`:
// reaching deeper means naming another pattern.
func (d *Dir) Glob(_ context.Context, gitRef, pattern string) ([]string, error) {
	if err := d.check(gitRef); err != nil {
		return nil, err
	}
	matches, err := fs.Glob(d.root.FS(), pattern)
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
