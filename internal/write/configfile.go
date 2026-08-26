package write

import (
	"context"
	"errors"
	"io/fs"

	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

func (w *Writer) configFile(ctx context.Context, filePath string) ([]byte, error) {
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
	contents, err := target.ReadFileContents(ctx, branch, filePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	return contents.Data, nil
}

func (w *Writer) setConfigFile(ctx context.Context, token, filePath, message string, body []byte, subject proof.Subject) (*Result, error) {
	target, err := w.Repositories.Target(ctx, w.ConfigRepository)
	if err != nil {
		return nil, err
	}
	branch, err := target.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}

	var replacing string
	var before []byte
	contents, err := target.ReadFileContents(ctx, branch, filePath)
	switch {
	case err == nil:
		replacing, before = contents.SHA, contents.Data
		if err := w.Proof.AuthorizeUpdateFrom(token, subject, duskmd.ContentHash(string(before))); err != nil {
			return nil, err
		}
	case errors.Is(err, fs.ErrNotExist):
		if err := w.Proof.AuthorizeCreate(token, subject); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}

	return w.land(ctx, change{
		target: target, repository: w.ConfigRepository, ref: filePath,
		before: before, created: before == nil,
		commit: githubapp.FileCommit{
			Branch: branch, Path: filePath, Message: message, Content: body,
			ReplacingSHA: replacing,
		},
	})
}
