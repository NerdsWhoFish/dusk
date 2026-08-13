package reconcile

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
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

// Tree walks the directory and reads its catalog files. It produces the same
// tree a tarball would, so `dusk validate` and the server cannot disagree.
func (d *Dir) Tree(_ context.Context, commit string) (*catalogfs.Tree, error) {
	if err := d.check(commit); err != nil {
		return nil, err
	}

	files := map[string][]byte{}
	err := fs.WalkDir(d.root.FS(), ".", func(candidate string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case entry.IsDir():
			if catalogfs.IsGitDir(candidate) {
				return fs.SkipDir
			}
			return nil
		case !catalogfs.IsCatalogFile(candidate):
			return nil
		}

		data, err := fs.ReadFile(d.root.FS(), candidate)
		if err != nil {
			return fmt.Errorf("read %q: %w", candidate, err)
		}
		files[candidate] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile: read %q: %w", d.gitRef, err)
	}
	return catalogfs.New(commit, files), nil
}

func (d *Dir) check(gitRef string) error {
	if gitRef != d.gitRef {
		return fmt.Errorf("reconcile: this directory is the tree for %q, not %q", d.gitRef, gitRef)
	}
	return nil
}
