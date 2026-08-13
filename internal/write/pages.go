package write

import (
	"context"
	"errors"
	"io/fs"

	"github.com/NerdsWhoFish/dusk/internal/page"
)

// Home returns the portal page the config repository declares, or
// fs.ErrNotExist when it declares none. It reads through the write path, so
// the UI renders what is committed rather than what a reconcile last indexed.
func (w *Writer) Home(ctx context.Context) ([]byte, error) {
	if w.ConfigRepository == "" {
		return nil, fs.ErrNotExist
	}

	target, err := w.Repositories.Target(ctx, w.ConfigRepository)
	if err != nil {
		return nil, err
	}
	branch, err := target.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}

	contents, err := target.ReadFileContents(ctx, branch, page.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	return contents.Data, nil
}
