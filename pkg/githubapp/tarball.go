package githubapp

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"
)

// Limits bound what an extraction accepts. A tarball is attacker-influenced
// input the moment an allowlisted account is compromised, and an unbounded
// extract fails as a filled disk rather than as an error.
type Limits struct {
	MaxFiles int
	MaxBytes int64
	MaxFile  int64
}

// DefaultLimits are generous for a catalog and nowhere near a real repository's
// worth of build output.
var DefaultLimits = Limits{
	MaxFiles: 20_000,
	MaxBytes: 256 << 20,
	MaxFile:  8 << 20,
}

// Tree is a repository's markdown at one commit, held in memory. Only what a
// reconcile can parse is kept; the rest of a repository would be weight carried
// for nothing.
type Tree struct {
	Commit string
	files  map[string][]byte
}

// Files returns every path held, in lexical order.
func (t *Tree) Files() []string {
	paths := make([]string, 0, len(t.files))
	for p := range t.files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return paths
}

// File returns one file's contents.
func (t *Tree) File(p string) ([]byte, bool) {
	data, ok := t.files[p]
	return data, ok
}

// Tarball downloads the repository at commit and keeps the markdown from it.
// One request replaces one per file, which is what makes a documentation tree
// or a directory of notes affordable to read at all.
func (r *Repository) Tarball(ctx context.Context, commit string, limits Limits) (*Tree, error) {
	resp, err := r.get(ctx, "/tarball/"+escapePath(commit), "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: tarball %s at %s: %w", r.slug(), short(commit), statusError(resp))
	}

	tree, err := extract(resp.Body, limits)
	if err != nil {
		return nil, fmt.Errorf("githubapp: tarball %s at %s: %w", r.slug(), short(commit), err)
	}
	tree.Commit = commit
	return tree, nil
}

// Extract reads a repository tarball into a Tree, dropping GitHub's wrapping
// directory and refusing anything that would escape it or exhaust the host.
func Extract(body io.Reader, commit string, limits Limits) (*Tree, error) {
	tree, err := extract(body, limits)
	if err != nil {
		return nil, err
	}
	tree.Commit = commit
	return tree, nil
}

// extract reads the markdown out of a gzipped tar, dropping GitHub's wrapping
// directory and refusing anything that would escape it or exhaust the host.
func extract(body io.Reader, limits Limits) (*Tree, error) {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return nil, fmt.Errorf("not gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tree := &Tree{files: map[string][]byte{}}
	reader := tar.NewReader(gz)
	var total int64
	var seen int

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return tree, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}

		seen++
		if seen > limits.MaxFiles {
			return nil, fmt.Errorf("more than %d entries", limits.MaxFiles)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}

		name, ok := repoPath(header.Name)
		if !ok || !isMarkdown(name) {
			continue
		}
		if header.Size > limits.MaxFile {
			return nil, fmt.Errorf("%q is larger than %d bytes", name, limits.MaxFile)
		}

		data, err := io.ReadAll(io.LimitReader(reader, limits.MaxFile+1))
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", name, err)
		}
		total += int64(len(data))
		if total > limits.MaxBytes {
			return nil, fmt.Errorf("markdown exceeds %d bytes in total", limits.MaxBytes)
		}
		tree.files[name] = data
	}
}

// repoPath strips the wrapping directory GitHub names after the commit, and
// refuses anything that would climb out of the tree.
func repoPath(name string) (string, bool) {
	_, rest, ok := strings.Cut(path.Clean(name), "/")
	if !ok || rest == "" {
		return "", false
	}
	if rest != path.Clean(rest) || strings.HasPrefix(rest, "../") || path.IsAbs(rest) {
		return "", false
	}
	for segment := range strings.SplitSeq(rest, "/") {
		if segment == ".." {
			return "", false
		}
	}
	return rest, true
}

func isMarkdown(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".md")
}
