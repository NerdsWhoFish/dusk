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
	LocateIn(ctx context.Context, gitRef, entityRef, repository string) (*index.Location, error)
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
	ObservedAs  []string
	Unset       []string

	// Decommissioned is three-state: absent leaves lifecycle alone, true
	// retires the declaration without deleting its history, and false makes it
	// active again by removing the lifecycle marker.
	Decommissioned *bool

	// Remove deletes an included declaration file. Confirm is deliberately
	// separate: a proof proves the caller read it, not that deletion was meant.
	Remove  bool
	Confirm bool

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
	// committed: the note, the vocabulary or the file's relations say it.
	Existing bool

	// Removed distinguishes a committed deletion from an ordinary update.
	Removed bool

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
		Created: c.created, Removed: c.commit.Delete, Mode: mode,
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

	location, err := w.Catalog.LocateIn(ctx, "", ref, declaration.Repository)
	if errors.Is(err, index.ErrNotFound) {
		if declaration.Remove {
			return nil, fmt.Errorf("write: nothing declares %s%s, so there is nothing to remove",
				ref, inRepository(declaration.Repository))
		}
		// A repository selector on an existing ref is a request to change that
		// exact copy, not permission to create a duplicate in the named repo.
		if declaration.Repository != "" {
			if elsewhere, locateErr := w.Catalog.Locate(ctx, "", ref); locateErr == nil {
				return nil, fmt.Errorf("write: %s is declared in %s, not %s. Omit repository to change it there, or choose a repository named by `changes` to resolve a duplicate",
					ref, elsewhere.Repository, declaration.Repository)
			}
		}
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
	if err := currentDeclaration(ref, at, contents.Data); err != nil {
		return nil, err
	}

	if declaration.Remove {
		if !declaration.Confirm {
			return nil, fmt.Errorf("write: removing %s deletes %s and its outbound relations; pass confirm only if deletion, rather than decommissioning, is intended", ref, at.Path)
		}
		if at.Path == RootFile {
			return nil, fmt.Errorf("write: %s is the root declaration. Removing %s would opt the whole repository out and remove everything it includes, so one entity operation cannot do it", ref, RootFile)
		}
		return w.land(ctx, change{
			target: target, repository: at.Repository, ref: ref, before: contents.Data,
			commit: githubapp.FileCommit{
				Branch: branch, Path: at.Path,
				Message:      fmt.Sprintf("declare: remove %s", ref),
				ReplacingSHA: contents.SHA,
				Delete:       true,
			},
		})
	}

	if err := apply(file, declaration); err != nil {
		return nil, err
	}

	rendered, err := render(file, at.Path)
	if err != nil {
		return nil, err
	}
	if string(rendered) == string(contents.Data) {
		return &Result{Ref: ref, Repository: at.Repository, Path: at.Path, Existing: true}, nil
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

func currentDeclaration(ref string, at *index.Location, contents []byte) error {
	if at.ContentHash != "" && duskmd.FileContentHash(contents) == at.ContentHash {
		return nil
	}
	return &proof.Rejection{
		Code:   proof.CodeStale,
		Ref:    ref,
		Detail: "the declaring file changed in Git after the catalog read that issued this token",
		Fix:    fmt.Sprintf("get(%q)", ref),
	}
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
	if len(declaration.Unset) > 0 {
		return nil, fmt.Errorf("write: unset changes an existing declaration; %s does not exist yet", ref)
	}
	if err := apply(created, declaration); err != nil {
		return nil, err
	}

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
func apply(file *duskmd.File, declaration Declaration) error {
	unset, err := unsetFields(declaration)
	if err != nil {
		return err
	}
	applyEntityFields(file, declaration, unset)
	return applyEntityAttributes(file, declaration, unset)
}

func applyEntityFields(file *duskmd.File, declaration Declaration, unset map[string]bool) {
	if declaration.Title != "" {
		file.Entity.Title = declaration.Title
	}
	if declaration.Description != "" {
		file.Entity.Description = declaration.Description
	}
	if unset["title"] {
		file.Entity.Title = ""
	}
	if unset["description"] {
		file.Entity.Description = ""
	}
	if declaration.ObservedAs != nil {
		file.ObservedAs = append([]string(nil), declaration.ObservedAs...)
	}
	if unset["observed_as"] {
		file.ObservedAs = nil
	}
}

func applyEntityAttributes(file *duskmd.File, declaration Declaration, unset map[string]bool) error {
	merged := file.Entity.GetAttributes().AsMap()
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range declaration.Attributes {
		merged[key] = value
	}
	for field := range unset {
		if key, ok := strings.CutPrefix(field, "attributes."); ok {
			delete(merged, key)
		}
	}
	if declaration.Decommissioned != nil {
		if *declaration.Decommissioned {
			merged[duskmd.LifecycleAttribute] = duskmd.LifecycleDecommissioned
		} else {
			delete(merged, duskmd.LifecycleAttribute)
		}
	}
	attributes, err := structpb.NewStruct(merged)
	if err != nil {
		return fmt.Errorf("write: declaration attributes: %w", err)
	}
	file.Entity.Attributes = attributes
	return nil
}

// unsetFields validates and indexes declaration fields to remove. The caller
// gets an error for a misspelling rather than a successful write that changed
// nothing.
func unsetFields(declaration Declaration) (map[string]bool, error) {
	unset := make(map[string]bool, len(declaration.Unset))
	for _, raw := range declaration.Unset {
		field := strings.TrimSpace(raw)
		switch {
		case field == "title", field == "description", field == "observed_as":
		case strings.HasPrefix(field, "attributes.") && len(field) > len("attributes."):
		default:
			return nil, fmt.Errorf("write: cannot unset %q; expected title, description, observed_as, or attributes.<name>", raw)
		}
		if field == "observed_as" && declaration.ObservedAs != nil {
			return nil, errors.New("write: observed_as cannot be replaced and unset in the same declaration")
		}
		if key, ok := strings.CutPrefix(field, "attributes."); ok {
			if _, set := declaration.Attributes[key]; set {
				return nil, fmt.Errorf("write: attribute %q cannot be set and unset in the same declaration", key)
			}
		}
		unset[field] = true
	}
	return unset, nil
}

func inRepository(repository string) string {
	if repository == "" {
		return ""
	}
	return " in " + repository
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
