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

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/FetchHQ/dusk/internal/index"
	"github.com/FetchHQ/dusk/pkg/duskmd"
)

// RootFile is the file whose presence opts a repository into the catalog.
const RootFile = "dusk.md"

// Source reads a repository's files at a git ref. It is the boundary ADR-0005
// requires: no GitHub type crosses it, so the reconciler is identical over a
// local directory and a remote repository.
type Source interface {
	// ReadFile returns the contents of filePath at gitRef. A file that is not
	// there yields an error satisfying errors.Is(err, fs.ErrNotExist).
	ReadFile(ctx context.Context, gitRef, filePath string) ([]byte, error)

	// Glob returns the paths at gitRef matching pattern, in lexical order.
	Glob(ctx context.Context, gitRef, pattern string) ([]string, error)
}

// Graph is what a repository declares at one git ref.
type Graph struct {
	// GitRef is the ref the graph was read at.
	GitRef string

	// Participating is false when the repository has no root dusk.md, which is
	// how it declines to be in the catalog rather than an error.
	Participating bool

	// Files are the catalog files read, index-aligned with Entities, since a
	// file declares exactly one entity.
	Files []string

	Entities  []*duskv1alpha1.Entity
	Relations []*duskv1alpha1.Relation
}

// Loader reads a repository into a graph, storing nothing.
type Loader struct {
	source Source
}

// NewLoader returns a loader reading from source.
func NewLoader(source Source) *Loader {
	return &Loader{source: source}
}

// Load reads the repository at gitRef and returns what it declares.
func (l *Loader) Load(ctx context.Context, gitRef string, provenance duskmd.Provenance) (*Graph, error) {
	root, err := l.readRoot(ctx, gitRef, provenance)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return &Graph{GitRef: gitRef, Participating: false}, nil
	}

	files, err := l.collect(ctx, gitRef, root, provenance)
	if err != nil {
		return nil, err
	}

	graph := &Graph{GitRef: gitRef, Participating: true, Files: make([]string, 0, len(files))}
	if err := graph.merge(files); err != nil {
		return nil, err
	}
	return graph, nil
}

// readRoot returns nil when the repository has no root dusk.md.
func (l *Loader) readRoot(ctx context.Context, gitRef string, provenance duskmd.Provenance) (*duskmd.File, error) {
	data, err := l.source.ReadFile(ctx, gitRef, RootFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reconcile: read %s at %q: %w", RootFile, gitRef, err)
	}

	root, err := duskmd.ParseRoot(RootFile, data, provenance)
	if err != nil {
		return nil, fmt.Errorf("reconcile: %q: %w", gitRef, err)
	}
	return root, nil
}

// collect returns the root file followed by everything its includes reach.
func (l *Loader) collect(ctx context.Context, gitRef string, root *duskmd.File, provenance duskmd.Provenance) ([]*duskmd.File, error) {
	paths, err := l.expand(ctx, gitRef, root.Include)
	if err != nil {
		return nil, err
	}

	files := make([]*duskmd.File, 0, len(paths)+1)
	files = append(files, root)

	var problems []error
	for _, filePath := range paths {
		data, err := l.source.ReadFile(ctx, gitRef, filePath)
		if err != nil {
			problems = append(problems, fmt.Errorf("reconcile: read %q at %q: %w", filePath, gitRef, err))
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
		return nil, errors.Join(problems...)
	}
	return files, nil
}

// expand resolves include patterns to a sorted, deduplicated path list. The
// root file is excluded so that a pattern matching it cannot declare its entity
// twice.
func (l *Loader) expand(ctx context.Context, gitRef string, patterns []string) ([]string, error) {
	seen := map[string]bool{RootFile: true}
	var paths []string

	for _, pattern := range patterns {
		matches, err := l.source.Glob(ctx, gitRef, pattern)
		if err != nil {
			return nil, fmt.Errorf("reconcile: expand %q at %q: %w", pattern, gitRef, err)
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
		g.Entities = append(g.Entities, file.Entity)
		g.Relations = append(g.Relations, file.Relations...)
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
func (r *Reconciler) Reconcile(ctx context.Context, gitRef string, provenance duskmd.Provenance) (*Graph, error) {
	graph, err := r.loader.Load(ctx, gitRef, provenance)
	if err != nil {
		return nil, err
	}
	if !graph.Participating {
		if err := r.index.DropGitRef(ctx, gitRef); err != nil {
			return nil, err
		}
		return graph, nil
	}
	if err := r.index.Put(ctx, gitRef, graph.Entities, graph.Relations); err != nil {
		return nil, err
	}
	return graph, nil
}
