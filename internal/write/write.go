// Package write turns an agent's declaration into a commit.
//
// It is where the three halves of the write path meet: the proof gate decides
// whether the agent may write, the parser and formatter decide what the file
// becomes, and the repository decides where the commit lands.
package write

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/store"
	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/textdiff"
)

// RootFile is the file a repository opts in with, and the only one guaranteed
// to exist in a participating repository.
const RootFile = "dusk.md"

// Target is one repository Dusk may write to.
type Target interface {
	ReadFileContents(ctx context.Context, ref, filePath string) (*githubapp.FileContents, error)
	CommitFile(ctx context.Context, commit githubapp.FileCommit) (*githubapp.Commit, error)
	DefaultBranch(ctx context.Context) (string, error)
}

// Repositories resolves a repository to something writable. A slug Dusk has no
// installation for is not writable, which is most of GitHub.
type Repositories interface {
	Target(ctx context.Context, slug string) (Target, error)
}

// Catalog is the slice of the index a write needs to route itself.
type Catalog interface {
	Locate(ctx context.Context, gitRef, entityRef string) (*index.Location, error)
	Get(ctx context.Context, gitRef, entityRef string) (*duskv1alpha1.Entity, error)

	// SimilarNotes is what turns a second copy of somebody's knowledge into a
	// warning naming the first, rather than a note nobody will find twice.
	SimilarNotes(ctx context.Context, gitRef, body string, limit int) ([]index.Similarity, error)
}

// Access reports how much Dusk may do to the repositories it watches. It is
// asked at write time rather than held, because onboarding is what chooses the
// mode and may not have happened when the Writer was built.
type Access interface {
	Mode() (store.AccessMode, error)
}

// Writer performs declarations.
type Writer struct {
	Catalog      Catalog
	Repositories Repositories
	Proof        *proof.Store
	Now          func() time.Time

	// Access decides whether a write becomes a commit or a proposal. A nil one
	// commits: the App's own permissions are the real gate, and not knowing the
	// mode is no reason to refuse a write GitHub would accept.
	Access Access

	// ConfigRepository is "owner/name" of where notes are written. Empty
	// disables note writing rather than defaulting somewhere surprising.
	ConfigRepository string
}

// Declaration is what an agent asked to change. Absent fields are left alone,
// so a caller setting one attribute does not blank the description.
type Declaration struct {
	Ref         string
	Title       string
	Description string
	Attributes  map[string]string

	// Repository is required only when creating, because an existing entity
	// already has a home and a new one does not.
	Repository string
}

// Result is where a declaration landed, so an agent can hand a human a link
// rather than asserting it worked.
type Result struct {
	Ref        string
	Repository string
	Path       string
	Commit     string
	URL        string
	Created    bool

	// Proposed means nothing was committed, because Dusk was not granted a
	// write, and Diff is the change it would have made instead.
	Proposed bool
	Diff     string

	// Mode is what Dusk was registered to do, which is why a proposal is one.
	Mode store.AccessMode

	// Existing means what was asked for is already written down, so nothing was
	// committed: the note already says this, or the vocabulary already does.
	Existing bool

	// Similar names notes that nearly say this already. Notes only: an entity
	// is deduplicated by its ref.
	Similar []index.Similarity
}

// change is one file write, before Dusk decides whether it may make it.
type change struct {
	target     Target
	repository string
	ref        string
	commit     githubapp.FileCommit

	// before is the file as it stands, and nil when it does not exist yet. It
	// is what a proposed diff is against.
	before []byte

	created bool
}

// land commits a change, or returns it as a proposal when Dusk may not write.
// Every write goes through here, so no path can forget that a mode which cannot
// commit still owes the caller the change it would have made (ADR-0052).
func (w *Writer) land(ctx context.Context, c change) (*Result, error) {
	mode, err := w.mode()
	if err != nil {
		return nil, err
	}

	result := &Result{
		Ref: c.ref, Repository: c.repository, Path: c.commit.Path,
		Created: c.created, Mode: mode,
	}
	if mode != store.ModeWrite {
		result.Proposed = true
		result.Diff = textdiff.Unified(c.commit.Path, c.before, c.commit.Content)
		return result, nil
	}

	commit, err := c.target.CommitFile(ctx, c.commit)
	if err != nil {
		return nil, err
	}
	result.Commit, result.URL = commit.SHA, commit.URL
	return result, nil
}

// mode reports what Dusk was granted, and write when nothing says.
func (w *Writer) mode() (store.AccessMode, error) {
	if w.Access == nil {
		return store.ModeWrite, nil
	}
	return w.Access.Mode()
}

// Declare creates or updates an entity in the repository that owns it.
func (w *Writer) Declare(ctx context.Context, token string, declaration Declaration) (*Result, error) {
	ref := strings.TrimSpace(declaration.Ref)
	if ref == "" {
		return nil, errors.New("write: a ref of the form kind:namespace/name is required")
	}

	location, err := w.Catalog.Locate(ctx, "", ref)
	if errors.Is(err, index.ErrNotFound) {
		return w.create(ctx, token, ref, declaration)
	}
	if err != nil {
		return nil, err
	}
	return w.update(ctx, token, ref, declaration, location)
}

func (w *Writer) update(ctx context.Context, token, ref string, declaration Declaration, at *index.Location) (*Result, error) {
	if err := w.Proof.AuthorizeUpdate(token, proof.Entity(ref), at.Version); err != nil {
		return nil, err
	}

	target, err := w.Repositories.Target(ctx, at.Repository)
	if err != nil {
		return nil, err
	}
	branch, err := target.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}

	file, contents, err := w.read(ctx, target, branch, at.Path)
	if err != nil {
		return nil, err
	}

	apply(file, declaration)

	rendered, err := render(file, at.Path)
	if err != nil {
		return nil, err
	}

	return w.land(ctx, change{
		target:     target,
		repository: at.Repository,
		ref:        ref,
		before:     contents.Data,
		commit: githubapp.FileCommit{
			Branch:       branch,
			Path:         at.Path,
			Message:      fmt.Sprintf("declare: update %s", ref),
			Content:      rendered,
			ReplacingSHA: contents.SHA,
		},
	})
}

func (w *Writer) create(ctx context.Context, token, ref string, declaration Declaration) (*Result, error) {
	if err := w.Proof.AuthorizeCreate(token, proof.Entity(ref)); err != nil {
		return nil, err
	}
	if declaration.Repository == "" {
		return nil, fmt.Errorf("write: %s does not exist yet, so say which repository should declare it", ref)
	}

	target, err := w.Repositories.Target(ctx, declaration.Repository)
	if err != nil {
		return nil, err
	}
	branch, err := target.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}

	// Reading the root does two jobs: it proves the repository has opted in,
	// and its includes decide where a new file may go.
	root, _, err := w.read(ctx, target, branch, RootFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("write: %s has no %s, so it is not in the catalog. Add one to opt it in; Dusk will not, because that file is how a repository consents", declaration.Repository, RootFile)
	}
	if err != nil {
		return nil, err
	}

	kind, namespace, name, err := splitRef(ref)
	if err != nil {
		return nil, err
	}

	filePath, err := placeIn(root, name)
	if err != nil {
		return nil, fmt.Errorf("write: %s: %w", declaration.Repository, err)
	}

	created := &duskmd.File{
		Path: filePath,
		Entity: &duskv1alpha1.Entity{
			Ref: ref, Kind: kind, Namespace: namespace, Name: name,
			Title:       declaration.Title,
			Description: declaration.Description,
		},
	}
	apply(created, declaration)

	rendered, err := duskmd.FormatIncluded(created, root.Entity.GetNamespace())
	if err != nil {
		return nil, err
	}

	return w.land(ctx, change{
		target:     target,
		repository: declaration.Repository,
		ref:        ref,
		created:    true,
		commit: githubapp.FileCommit{
			Branch:  branch,
			Path:    filePath,
			Message: fmt.Sprintf("declare: add %s", ref),
			Content: rendered,
		},
	})
}

// read fetches and parses one catalog file, resolving the root's namespace
// first when the file is an included one that inherits it.
func (w *Writer) read(ctx context.Context, target Target, branch, filePath string) (*duskmd.File, *githubapp.FileContents, error) {
	contents, err := target.ReadFileContents(ctx, branch, filePath)
	if err != nil {
		return nil, nil, err
	}

	provenance := duskmd.Provenance{Version: branch, ObservedAt: w.now()}
	if filePath == RootFile {
		file, err := duskmd.ParseRoot(filePath, contents.Data, provenance)
		return file, contents, err
	}

	root, err := target.ReadFileContents(ctx, branch, RootFile)
	if err != nil {
		return nil, nil, fmt.Errorf("write: read %s to resolve the namespace: %w", RootFile, err)
	}
	parsedRoot, err := duskmd.ParseRoot(RootFile, root.Data, provenance)
	if err != nil {
		return nil, nil, err
	}

	file, err := duskmd.ParseIncluded(filePath, contents.Data, parsedRoot.Entity.GetNamespace(), provenance)
	return file, contents, err
}

// render writes a file back, inheriting nothing for the root and the root's
// namespace for anything else.
func render(file *duskmd.File, filePath string) ([]byte, error) {
	if filePath == RootFile {
		return duskmd.FormatRoot(file)
	}
	return duskmd.FormatIncluded(file, file.Entity.GetNamespace())
}

// apply overlays what the declaration set. An absent field is left alone, so
// setting one attribute cannot blank a description somebody wrote.
func apply(file *duskmd.File, declaration Declaration) {
	if declaration.Title != "" {
		file.Entity.Title = declaration.Title
	}
	if declaration.Description != "" {
		file.Entity.Description = declaration.Description
	}
	if len(declaration.Attributes) == 0 {
		return
	}

	merged := file.Entity.GetAttributes().AsMap()
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range declaration.Attributes {
		merged[key] = value
	}
	// Values arrive as strings, so this cannot fail on an unrepresentable type.
	if attributes, err := structpb.NewStruct(merged); err == nil {
		file.Entity.Attributes = attributes
	}
}

// placeIn picks a new file's path from the root's includes, through the matcher
// that reads them. A file they do not reach is committed and never read: the
// write succeeds and the catalog never changes.
func placeIn(root *duskmd.File, name string) (string, error) {
	if len(root.Include) == 0 {
		return "", fmt.Errorf("its %s has no include, so a new file would be committed and never read. Add one, such as `services/*/dusk.md`", RootFile)
	}

	for _, pattern := range root.Include {
		if placed, ok := catalogfs.Place(pattern, name); ok {
			return placed, nil
		}
	}
	return "", fmt.Errorf("none of its include patterns %v can name a file for %q", root.Include, name)
}

func splitRef(ref string) (kind, namespace, name string, err error) {
	kind, rest, ok := strings.Cut(ref, ":")
	if !ok {
		return "", "", "", fmt.Errorf("write: %q is not a ref of the form kind:namespace/name", ref)
	}
	namespace, name, ok = strings.Cut(rest, "/")
	if !ok || name == "" {
		return "", "", "", fmt.Errorf("write: %q is not a ref of the form kind:namespace/name", ref)
	}
	return kind, namespace, name, nil
}

func (w *Writer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}
