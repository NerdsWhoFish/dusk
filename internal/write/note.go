package write

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
)

// NoteDir is where a written note lands. ADR-0031 makes it always in scope, so
// a note put here is read back without anything being added to an include.
const NoteDir = ".dusk"

// Note is what an agent asked to record.
type Note struct {
	// Id is the note's path, and is empty when creating one. ADR-0031 makes the
	// path the id, so updating a note means naming where it already lives.
	Id string

	Kind   string
	Refs   []string
	Body   string
	Pinned bool

	// Status closes a note that is work: open, done or dropped. Empty leaves it
	// as it was, so changing a body does not reopen something finished.
	Status string
}

// ErrNoConfigRepository reports that Dusk has not been told where notes live.
// Reads and entity writes do not need one, so this is not a boot failure.
var ErrNoConfigRepository = errors.New("write: no config repository is configured, so there is nowhere to put a note. Set DUSK_CONFIG_REPOSITORY to owner/name of a repository that has a dusk.md")

// NoteDestination reports where notes are written, or empty if nowhere.
func (w *Writer) NoteDestination() string { return w.ConfigRepository }

// Record creates or updates a note in the config repository.
func (w *Writer) Record(ctx context.Context, token string, note Note) (*Result, error) {
	if w.ConfigRepository == "" {
		return nil, ErrNoConfigRepository
	}

	// Only a new note has to carry everything. An update merges over what the
	// file already says, so demanding a kind again to change a body would make
	// every partial edit a chance to get one of them wrong.
	if note.Id == "" {
		if strings.TrimSpace(note.Kind) == "" {
			return nil, fmt.Errorf("write: a note needs a kind, one of %s", strings.Join(duskmd.WellKnownNoteKinds, ", "))
		}
		if strings.TrimSpace(note.Body) == "" {
			return nil, errors.New("write: a note is its prose, and this one has none")
		}
	}

	target, err := w.Repositories.Target(ctx, w.ConfigRepository)
	if err != nil {
		return nil, err
	}
	branch, err := target.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}

	// Reading the root proves the config repository opted in. Without it a note
	// would be committed and then never read back, which fails silently.
	if _, _, err := w.read(ctx, target, branch, RootFile); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("write: the config repository %s has no %s, so a note written there would never be read back. Add one; Dusk will not, because that file is how a repository consents", w.ConfigRepository, RootFile)
		}
		return nil, err
	}

	if note.Id != "" {
		return w.updateNote(ctx, token, target, branch, note)
	}
	return w.createNote(ctx, target, branch, note)
}

func (w *Writer) updateNote(ctx context.Context, token string, target Target, branch string, note Note) (*Result, error) {
	filePath, err := notePath(note.Id)
	if err != nil {
		return nil, err
	}

	contents, err := target.ReadFileContents(ctx, branch, filePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("write: no note at %s. Omit the id to create one", filePath)
	}
	if err != nil {
		return nil, err
	}

	existing, err := duskmd.ParseNote(filePath, contents.Data, duskmd.Provenance{Version: branch, ObservedAt: w.now()})
	if err != nil {
		return nil, err
	}
	if err := w.Proof.AuthorizeUpdate(token, filePath, existing.GetContentHash()); err != nil {
		return nil, err
	}

	rendered, err := duskmd.FormatNote(merge(existing, note))
	if err != nil {
		return nil, err
	}

	commit, err := target.CommitFile(ctx, githubapp.FileCommit{
		Branch:       branch,
		Path:         filePath,
		Message:      "note: update " + filePath,
		Content:      rendered,
		ReplacingSHA: contents.SHA,
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Ref: filePath, Repository: w.ConfigRepository, Path: filePath,
		Commit: commit.SHA, URL: commit.URL,
	}, nil
}

// createNote writes a new note. There is no proof token here: a note is new
// prose rather than a change to something an agent should have read first, and
// ADR-0009's gate exists to stop blind overwrites, which a create cannot do.
func (w *Writer) createNote(ctx context.Context, target Target, branch string, note Note) (*Result, error) {
	filePath := path.Join(NoteDir, slug(note.Kind)+"-"+duskmd.ContentHash(note.Body)[:8]+".md")

	rendered, err := duskmd.FormatNote(&duskv1alpha1.Note{
		Kind: note.Kind, Refs: note.Refs, Body: note.Body,
		Pinned: note.Pinned, Status: note.Status,
	})
	if err != nil {
		return nil, err
	}

	commit, err := target.CommitFile(ctx, githubapp.FileCommit{
		Branch:  branch,
		Path:    filePath,
		Message: "note: add " + filePath,
		Content: rendered,
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Ref: filePath, Repository: w.ConfigRepository, Path: filePath,
		Commit: commit.SHA, URL: commit.URL, Created: true,
	}, nil
}

// merge applies what the agent supplied over what the file already said, so
// changing a note's body does not blank the refs it attaches to.
func merge(existing *duskv1alpha1.Note, note Note) *duskv1alpha1.Note {
	merged := &duskv1alpha1.Note{
		Kind:   existing.GetKind(),
		Refs:   existing.GetRefs(),
		Body:   existing.GetBody(),
		Pinned: note.Pinned,
		Status: existing.GetStatus(),
	}
	if strings.TrimSpace(note.Status) != "" {
		merged.Status = note.Status
	}
	if strings.TrimSpace(note.Kind) != "" {
		merged.Kind = note.Kind
	}
	if note.Refs != nil {
		merged.Refs = note.Refs
	}
	if strings.TrimSpace(note.Body) != "" {
		merged.Body = note.Body
	}
	return merged
}

// notePath refuses an id that would escape the notes directory. The id arrives
// from an agent, which is the same trust level as a request body.
func notePath(id string) (string, error) {
	cleaned := path.Clean(strings.TrimSpace(id))
	if cleaned != strings.TrimSpace(id) || path.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("write: %q is not a note id", id)
	}
	if !strings.HasSuffix(cleaned, ".md") {
		return "", fmt.Errorf("write: %q is not a note id: a note is a markdown file", id)
	}
	return cleaned, nil
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(kind string) string {
	cleaned := notSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(kind)), "-")
	if trimmed := strings.Trim(cleaned, "-"); trimmed != "" {
		return trimmed
	}
	return "note"
}
