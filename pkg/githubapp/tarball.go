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
	"strings"

	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
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

// Tarball downloads the repository at commit and keeps the catalog files from
// it. One request replaces one per file, which is what makes a documentation
// tree or a directory of notes affordable to read at all.
func (r *Repository) Tarball(ctx context.Context, commit string, limits Limits) (*catalogfs.Tree, error) {
	resp, err := r.get(ctx, "/tarball/"+escapePath(commit), "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: tarball %s at %s: %w", r.slug(), short(commit), statusError(resp))
	}

	tree, err := Extract(resp.Body, commit, limits)
	if err != nil {
		return nil, fmt.Errorf("githubapp: tarball %s at %s: %w", r.slug(), short(commit), err)
	}
	return tree, nil
}

// Extract reads a repository tarball into a tree, dropping GitHub's wrapping
// directory and refusing anything that would escape it or exhaust the host.
func Extract(body io.Reader, commit string, limits Limits) (*catalogfs.Tree, error) {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return nil, fmt.Errorf("not gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	files := map[string][]byte{}
	reader := tar.NewReader(gz)
	var total int64
	var seen int

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return catalogfs.New(commit, files), nil
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
		if !ok || !catalogfs.IsCatalogFile(name) {
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
		files[name] = data
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
