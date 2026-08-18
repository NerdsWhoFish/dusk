// Package reconcile turns a repository at a git ref into the materialized graph.
//
// It is the seam between the `dusk.md` parser and the index, and it is where
// ADR-0004's promise is kept: the root file plus whatever that file explicitly
// points at, and nothing else in the repository is ever read.
//
// Reading and storing are separate. A Loader produces a graph from a source and
// touches no storage, which is what lets a local checkout be validated with no
// database and no server. A Reconciler stores what a Loader read.
//
// It is internal because it depends on the index, whose schema is disposable.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// RootFile is the file whose presence opts a repository into the catalog.
const RootFile = "dusk.md"

// Source reads a repository's files at a git ref. It is the boundary ADR-0005
// requires: no GitHub type crosses it, so the reconciler is identical over a
// local directory and a remote repository.
type Source interface {
	// Resolve returns the commit a ref points at. Every read in one reconcile
	// is then made against that commit, so a branch moving partway through
	// cannot stitch two commits into one graph.
	Resolve(ctx context.Context, gitRef string) (string, error)

	// Tree returns the catalog files at commit. Producing this is a source's
	// only real job: matching and reading over it are identical everywhere, so
	// they live on the tree and a change to either reaches every source.
	Tree(ctx context.Context, commit string) (*catalogfs.Tree, error)
}

// Graph is what a repository declares at one git ref.
type Graph struct {
	// Repository is the owner/name this graph was read from, and the scope it
	// is stored under. Many repositories share one catalog, so it partitions
	// the index alongside GitRef.
	Repository string

	// GitRef is the ref the graph was read at, and the key it is stored under.
	GitRef string

	// Commit is what GitRef resolved to. It is recorded as provenance, so a
	// claim in the catalog can be traced to the exact tree that made it.
	Commit string

	// Participating is false when the repository has no root dusk.md, which is
	// how it declines to be in the catalog rather than an error.
	Participating bool

	// Files are the catalog files read, index-aligned with Entities, since a
	// file declares exactly one entity.
	Files []string

	// ContentHashes are the exact file identities index-aligned with Entities.
	ContentHashes []string

	Entities  []*duskv1alpha1.Entity
	Relations []*duskv1alpha1.Relation

	// ObservedAs is index-aligned with Entities: what an ingester calls each
	// one, so drift can tell a different name from a disappearance.
	ObservedAs [][]string

	// Notes are the curated knowledge attached to entities, which may live in
	// this repository while the entities they describe live elsewhere.
	Notes []*duskv1alpha1.Note

	// Kinds are what this repository mints, from the reserved vocabulary file.
	// They are read here so `dusk validate` catches a bad one locally.
	Kinds []vocab.Kind
}

// declarations pairs each entity with the file that declared it. Files and
// Entities are appended together, so their indexes line up.
func (g *Graph) declarations() []index.Declaration {
	out := make([]index.Declaration, 0, len(g.Entities))
	for i, entity := range g.Entities {
		out = append(out, index.Declaration{
			Path: g.Files[i], ContentHash: g.ContentHashes[i], Entity: entity, ObservedAs: g.ObservedAs[i],
		})
	}
	return out
}

// Loader reads a repository into a graph, storing nothing.
type Loader struct {
	source Source
}

// NewLoader returns a loader reading from source.
func NewLoader(source Source) *Loader {
	return &Loader{source: source}
}

// Load reads the repository at gitRef and returns what it declares. The ref is
// resolved once and every file read at the resulting commit, so a branch moving
// partway through cannot produce a graph stitched from two trees.
func (l *Loader) Load(ctx context.Context, gitRef string, observedAt time.Time) (*Graph, error) {
	commit, err := l.source.Resolve(ctx, gitRef)
	if err != nil {
		return nil, fmt.Errorf("reconcile: resolve %q: %w", gitRef, err)
	}
	provenance := duskmd.Provenance{Version: commit, ObservedAt: observedAt}

	tree, err := l.source.Tree(ctx, commit)
	if errors.Is(err, ErrNotParticipating) {
		return &Graph{GitRef: gitRef, Commit: commit, Participating: false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reconcile: read %q at %q: %w", gitRef, commit, err)
	}

	root, err := readRoot(tree, commit, provenance)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return &Graph{GitRef: gitRef, Commit: commit, Participating: false}, nil
	}

	files, notes, err := collect(tree, commit, root, provenance)
	if err != nil {
		return nil, err
	}

	kinds, err := readKinds(tree)
	if err != nil {
		return nil, err
	}

	graph := &Graph{
		GitRef: gitRef, Commit: commit, Participating: true,
		Files: make([]string, 0, len(files)),
		Notes: notes,
		Kinds: kinds,
	}
	if err := graph.merge(files); err != nil {
		return nil, err
	}
	return graph, nil
}

// readRoot returns nil when the repository has no root dusk.md.
func readRoot(tree *catalogfs.Tree, commit string, provenance duskmd.Provenance) (*duskmd.File, error) {
	data, err := tree.Read(RootFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reconcile: read %s at %q: %w", RootFile, commit, err)
	}

	root, err := duskmd.ParseRoot(RootFile, data, provenance)
	if err != nil {
		return nil, fmt.Errorf("reconcile: %q: %w", commit, err)
	}
	return root, nil
}

// readKinds reads the vocabulary a repository mints, which is absent from most
// of them. A malformed one fails the reconcile rather than minting silently
// nothing, because the whole point of a role is that something acts on it.
func readKinds(tree *catalogfs.Tree) ([]vocab.Kind, error) {
	data, err := tree.Read(vocab.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reconcile: read %s: %w", vocab.Path, err)
	}

	kinds, err := duskmd.ParseKinds(vocab.Path, data)
	if err != nil {
		return nil, err
	}
	return kinds.Kinds, nil
}

// collect returns the root file followed by everything its includes reach,
// separated into the entities they declare and the notes they carry.
func collect(tree *catalogfs.Tree, commit string, root *duskmd.File, provenance duskmd.Provenance) ([]*duskmd.File, []*duskv1alpha1.Note, error) {
	paths, err := expand(tree, commit, root.Include)
	if err != nil {
		return nil, nil, err
	}

	files := make([]*duskmd.File, 0, len(paths)+1)
	files = append(files, root)

	var notes []*duskv1alpha1.Note
	var problems []error
	for _, filePath := range paths {
		// Dusk's own declared configuration lives in the directory that is
		// always in scope, and is read by what declared it. Parsing it as
		// catalog content fails the whole repository's reconcile (ADR-0048).
		if catalogfs.IsReserved(filePath) {
			continue
		}

		data, err := tree.Read(filePath)
		if err != nil {
			problems = append(problems, fmt.Errorf("reconcile: read %q at %q: %w", filePath, commit, err))
			continue
		}

		// An included file says which it is. Notes and entities share the
		// include list, so the discriminator is what tells them apart.
		if duskmd.IsNote(data) {
			note, err := duskmd.ParseNote(filePath, data, provenance)
			if err != nil {
				problems = append(problems, err)
				continue
			}
			notes = append(notes, note)
			continue
		}

		included, err := duskmd.ParseIncluded(filePath, data, root.Entity.GetNamespace(), provenance)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		files = append(files, included)
	}
	if len(problems) > 0 {
		return nil, nil, errors.Join(problems...)
	}
	return files, notes, nil
}

// DuskDir is read whenever it exists, with no include needed. A directory whose
// whole purpose is catalog content is consent by construction, and it gives a
// write somewhere to land in a repository that declares no include.
const DuskDir = catalogfs.DuskDir

// expand resolves include patterns to a sorted, deduplicated path list. The
// root file is excluded so that a pattern matching it cannot declare its entity
// twice.
func expand(tree *catalogfs.Tree, commit string, patterns []string) ([]string, error) {
	seen := map[string]bool{RootFile: true}
	var paths []string

	// Always in scope, ahead of whatever the root asks for.
	patterns = append([]string{DuskDir + "/**/*.md"}, patterns...)

	for _, pattern := range patterns {
		matches, err := tree.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("reconcile: expand %q at %q: %w", pattern, commit, err)
		}
		for _, match := range matches {
			match = path.Clean(match)
			if seen[match] {
				continue
			}
			seen[match] = true
			paths = append(paths, match)
		}
	}

	slices.Sort(paths)
	return paths, nil
}

// merge flattens parsed files, rejecting a doubly-declared entity.
func (g *Graph) merge(files []*duskmd.File) error {
	declaredIn := make(map[string]string, len(files))
	var problems []error

	for _, file := range files {
		ref := file.Entity.GetRef()
		if first, ok := declaredIn[ref]; ok {
			problems = append(problems, fmt.Errorf("reconcile: %q is declared by both %s and %s, and an entity has exactly one home", ref, first, file.Path))
			continue
		}
		declaredIn[ref] = file.Path
		g.Files = append(g.Files, file.Path)
		g.ContentHashes = append(g.ContentHashes, file.ContentHash)
		g.Entities = append(g.Entities, file.Entity)
		g.Relations = append(g.Relations, file.Relations...)
		g.ObservedAs = append(g.ObservedAs, file.ObservedAs)
	}

	return errors.Join(problems...)
}

// Reconciler stores what a Loader reads.
type Reconciler struct {
	loader *Loader
	index  *index.DB
}

// New returns a reconciler reading from source and writing to idx.
func New(source Source, idx *index.DB) *Reconciler {
	return &Reconciler{loader: NewLoader(source), index: idx}
}

// Reconcile reads the repository at gitRef and replaces everything the index
// holds for it. A repository with no root dusk.md is not an error: it has not
// opted in, and the previous contents at that ref are cleared.
func (r *Reconciler) Reconcile(ctx context.Context, repository, gitRef string, observedAt time.Time) (*Graph, error) {
	graph, err := r.loader.Load(ctx, gitRef, observedAt)
	if err != nil {
		return nil, err
	}
	graph.Repository = repository

	if !graph.Participating {
		if err := r.index.DropRepository(ctx, repository, gitRef); err != nil {
			return nil, err
		}
		return graph, nil
	}
	if err := r.index.PutCatalog(ctx, repository, gitRef, graph.declarations(), graph.Relations, graph.Notes, graph.Kinds); err != nil {
		return nil, err
	}
	return graph, nil
}
