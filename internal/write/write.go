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
	"path"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
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
}

// Writer performs declarations.
type Writer struct {
	Catalog      Catalog
	Repositories Repositories
	Proof        *proof.Store
	Now          func() time.Time

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
	if err := w.Proof.AuthorizeUpdate(token, ref, at.Version); err != nil {
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

	commit, err := target.CommitFile(ctx, githubapp.FileCommit{
		Branch:       branch,
		Path:         at.Path,
		Message:      fmt.Sprintf("declare: update %s", ref),
		Content:      rendered,
		ReplacingSHA: contents.SHA,
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Ref: ref, Repository: at.Repository, Path: at.Path,
		Commit: commit.SHA, URL: commit.URL,
	}, nil
}

func (w *Writer) create(ctx context.Context, token, ref string, declaration Declaration) (*Result, error) {
	if err := w.Proof.AuthorizeCreate(token, ref); err != nil {
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

	commit, err := target.CommitFile(ctx, githubapp.FileCommit{
		Branch:  branch,
		Path:    filePath,
		Message: fmt.Sprintf("declare: add %s", ref),
		Content: rendered,
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Ref: ref, Repository: declaration.Repository, Path: filePath,
		Commit: commit.SHA, URL: commit.URL, Created: true,
	}, nil
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

// placeIn picks a new file's path from the root's includes. A file they do not
// reach is committed and never read, which is the quietest failure available:
// the write succeeds and the catalog never changes.
func placeIn(root *duskmd.File, name string) (string, error) {
	if len(root.Include) == 0 {
		return "", fmt.Errorf("its %s has no include, so a new file would be committed and never read. Add one, such as `services/*/dusk.md`", RootFile)
	}

	for _, pattern := range root.Include {
		candidate := strings.Replace(pattern, "*", name, 1)
		if strings.Contains(candidate, "*") {
			continue
		}
		if ok, err := path.Match(pattern, candidate); err == nil && ok {
			return candidate, nil
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
