package write

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// ErrNoVocabularyRepository reports that Dusk has nowhere to keep a minted
// kind. Reading the vocabulary needs no repository, so this is not fatal.
var ErrNoVocabularyRepository = errors.New("write: no config repository is configured, so there is nowhere to keep a minted kind. Set DUSK_CONFIG_REPOSITORY to owner/name of a repository that has a dusk.md")

// Minted is the vocabulary file as it stands. An absent one is the empty
// vocabulary rather than an error, which is what lets the first mint authorize
// the same way every later one does.
type Minted struct {
	Kinds *duskmd.Kinds

	// Version is the hash a proof token records, the same shape the page uses.
	Version string

	// sha is what the commit replaces, so a raced mint collides.
	sha string
}

// Vocabulary returns what the config repository mints today.
func (w *Writer) Vocabulary(ctx context.Context) (*Minted, error) {
	if w.ConfigRepository == "" {
		return &Minted{Kinds: &duskmd.Kinds{}, Version: duskmd.ContentHash("")}, nil
	}

	target, branch, err := w.configTarget(ctx)
	if err != nil {
		return nil, err
	}
	return w.vocabulary(ctx, target, branch)
}

func (w *Writer) vocabulary(ctx context.Context, target Target, branch string) (*Minted, error) {
	contents, err := target.ReadFileContents(ctx, branch, vocab.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Minted{Kinds: &duskmd.Kinds{}, Version: duskmd.ContentHash("")}, nil
	}
	if err != nil {
		return nil, err
	}

	kinds, err := duskmd.ParseKinds(vocab.Path, contents.Data)
	if err != nil {
		return nil, err
	}
	return &Minted{
		Kinds:   kinds,
		Version: duskmd.ContentHash(string(contents.Data)),
		sha:     contents.SHA,
	}, nil
}

// MintKind adds a kind to the config repository's vocabulary. Its proof token
// has to come from reading that vocabulary, because minting without seeing
// what is there is how `svc` appears beside `service`.
func (w *Writer) MintKind(ctx context.Context, token string, kind vocab.Kind) (*Result, error) {
	if w.ConfigRepository == "" {
		return nil, ErrNoVocabularyRepository
	}

	target, branch, err := w.configTarget(ctx)
	if err != nil {
		return nil, err
	}

	// The root proves the repository opted in. Without it the file would be
	// committed and never read back, which fails silently.
	if _, _, err := w.read(ctx, target, branch, RootFile); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("write: the config repository %s has no %s, so a minted kind would never be read back. Add one; Dusk will not, because that file is how a repository consents", w.ConfigRepository, RootFile)
		}
		return nil, err
	}

	minted, err := w.vocabulary(ctx, target, branch)
	if err != nil {
		return nil, err
	}
	if err := w.Proof.AuthorizeUpdateFrom(token, proof.Vocabulary(vocab.Path), minted.Version); err != nil {
		return nil, err
	}
	if _, clash := vocab.Lookup(kind.Namespace, kind.Name, minted.Kinds.Kinds); clash {
		return nil, fmt.Errorf("write: %q is already minted as a %s kind, or is another spelling of one that is",
			kind.Name, kind.Namespace)
	}

	rendered, err := duskmd.FormatKinds(&duskmd.Kinds{
		Kinds: append(minted.Kinds.Kinds, kind),
		Body:  minted.Kinds.Body,
	})
	if err != nil {
		return nil, err
	}

	commit, err := target.CommitFile(ctx, githubapp.FileCommit{
		Branch:       branch,
		Path:         vocab.Path,
		Message:      fmt.Sprintf("kind: mint %s %s", kind.Namespace, kind.Name),
		Content:      rendered,
		ReplacingSHA: minted.sha,
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Ref: vocab.Path, Repository: w.ConfigRepository, Path: vocab.Path,
		Commit: commit.SHA, URL: commit.URL, Created: minted.sha == "",
	}, nil
}

func (w *Writer) configTarget(ctx context.Context) (Target, string, error) {
	target, err := w.Repositories.Target(ctx, w.ConfigRepository)
	if err != nil {
		return nil, "", err
	}
	branch, err := target.DefaultBranch(ctx)
	if err != nil {
		return nil, "", err
	}
	return target, branch, nil
}
