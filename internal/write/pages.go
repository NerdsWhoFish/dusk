package write

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/NerdsWhoFish/dusk/internal/page"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
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

// SetHome writes the portal page, which is how an agent curates the layout.
// It parses before committing: a page that cannot be read renders as nothing,
// and learning that from a blank homepage is worse than being refused.
func (w *Writer) SetHome(ctx context.Context, token string, body []byte) (*Result, error) {
	if w.ConfigRepository == "" {
		return nil, errors.New("write: no config repository is set, so there is nowhere to keep a page")
	}
	if _, _, err := page.Parse(body); err != nil {
		return nil, fmt.Errorf("write: that page would not render: %w", err)
	}
	target, err := w.Repositories.Target(ctx, w.ConfigRepository)
	if err != nil {
		return nil, err
	}
	branch, err := target.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}

	// The existing sha, so a page edited by two people collides rather than one
	// silently overwriting the other. Authorizing against it is the same
	// read-before-write contract every other write obeys (ADR-0009).
	var replacing string
	var before []byte
	if contents, err := target.ReadFileContents(ctx, branch, page.Path); err == nil {
		replacing, before = contents.SHA, contents.Data
		if err := w.Proof.AuthorizeUpdate(token, page.Path, duskmd.ContentHash(string(contents.Data))); err != nil {
			return nil, err
		}
	} else if err := w.Proof.AuthorizeCreate(token, page.Path); err != nil {
		return nil, err
	}

	return w.land(ctx, change{
		target:     target,
		repository: w.ConfigRepository,
		ref:        page.Path,
		before:     before,
		created:    before == nil,
		commit: githubapp.FileCommit{
			Branch:       branch,
			Path:         page.Path,
			Message:      "page: update " + page.Path,
			Content:      body,
			ReplacingSHA: replacing,
		},
	})
}
