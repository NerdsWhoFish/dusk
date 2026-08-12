package reconcile_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/FetchHQ/dusk/internal/reconcile"
	"github.com/FetchHQ/dusk/pkg/catalogfs"
	"github.com/FetchHQ/dusk/pkg/githubapp"
)

const commit = "a866a20af5b14f34b7fe993e1b1fa0b6df0a46b7"

// fakeDownloader counts what a reconcile actually costs, which is the whole
// point of ADR-0032.
type fakeDownloader struct {
	files      map[string]string
	noRootFile bool

	resolved  int
	probed    int
	downloads int
}

func (f *fakeDownloader) Resolve(context.Context, string) (string, error) {
	f.resolved++
	return commit, nil
}

func (f *fakeDownloader) ReadFileContents(_ context.Context, _, filePath string) (*githubapp.FileContents, error) {
	f.probed++
	if f.noRootFile {
		return nil, fmt.Errorf("%q: %w", filePath, fs.ErrNotExist)
	}
	return &githubapp.FileContents{Data: []byte("---\n"), SHA: "blob"}, nil
}

func (f *fakeDownloader) Tarball(_ context.Context, at string, limits githubapp.Limits) (*catalogfs.Tree, error) {
	f.downloads++
	return githubapp.Extract(bytes.NewReader(tarballOf(f.files)), at, limits)
}

// tarballOf builds what GitHub sends: a gzipped tar whose entries all sit under
// one wrapping directory named after the commit.
func tarballOf(files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{
			Name: "example-homelab-a866a20/" + name,
			Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestTarballReadsWithoutFurtherCalls(t *testing.T) {
	downloader := &fakeDownloader{files: map[string]string{
		"dusk.md":                   rootFile,
		"services/jellyfin/dusk.md": jellyfinFile,
		".dusk/transcoding.md":      noteFile,
		"README.md":                 "not a catalog file",
		"main.go":                   "package main",
	}}
	source := &reconcile.Tarball{Repo: downloader}

	tree, err := source.Tree(t.Context(), commit)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}

	t.Run("every file comes from the one download", func(t *testing.T) {
		for _, name := range []string{"dusk.md", "services/jellyfin/dusk.md", ".dusk/transcoding.md"} {
			if _, err := tree.Read(name); err != nil {
				t.Errorf("Read(%q): %v", name, err)
			}
		}
		if downloader.downloads != 1 {
			t.Errorf("downloaded %d times, want 1", downloader.downloads)
		}
	})

	t.Run("non-markdown is not carried", func(t *testing.T) {
		if _, err := tree.Read("main.go"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("main.go was kept, which is weight for nothing")
		}
	})

	// The loader asks again after the controller already has, and a second
	// download would double the cost of every reconcile.
	t.Run("asking again at the same commit does not download again", func(t *testing.T) {
		if _, err := source.Tree(t.Context(), commit); err != nil {
			t.Fatalf("Tree: %v", err)
		}
		if downloader.downloads != 1 {
			t.Errorf("downloaded %d times, want 1", downloader.downloads)
		}
	})
}

// A repository with no dusk.md has not opted in, so nothing about it is
// transferred. It is the overwhelming majority of any installation.
func TestARepositoryWithoutDuskMdIsNeverDownloaded(t *testing.T) {
	downloader := &fakeDownloader{noRootFile: true}
	source := &reconcile.Tarball{Repo: downloader}

	_, err := source.Tree(t.Context(), commit)
	if !errors.Is(err, reconcile.ErrNotParticipating) {
		t.Fatalf("Tree = %v, want ErrNotParticipating", err)
	}
	if downloader.downloads != 0 {
		t.Errorf("downloaded a repository that has not opted in")
	}
	if downloader.probed != 1 {
		t.Errorf("probed %d times, want exactly 1", downloader.probed)
	}
}

// The controller resolves to decide whether to reconcile and the loader
// resolves to pin its reads. Without memoizing, one ref would cost two calls.
func TestResolveIsMemoized(t *testing.T) {
	downloader := &fakeDownloader{files: map[string]string{"dusk.md": rootFile}}
	source := &reconcile.Tarball{Repo: downloader}

	for range 3 {
		if _, err := source.Resolve(t.Context(), "refs/heads/main"); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	if downloader.resolved != 1 {
		t.Errorf("resolved %d times, want 1", downloader.resolved)
	}
}

// A tarball is attacker-influenced input the moment an allowlisted account is
// compromised, so extraction refuses rather than filling the disk.
func TestExtractionIsBounded(t *testing.T) {
	t.Run("a path climbing out of the tree is dropped", func(t *testing.T) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		body := "owned"
		_ = tw.WriteHeader(&tar.Header{
			Name: "wrapper/../../etc/evil.md", Mode: 0o644,
			Size: int64(len(body)), Typeflag: tar.TypeReg,
		})
		_, _ = tw.Write([]byte(body))
		_ = tw.Close()
		_ = gz.Close()

		tree, err := githubapp.Extract(bytes.NewReader(buf.Bytes()), commit, githubapp.DefaultLimits)
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if slices.ContainsFunc(tree.Paths(), func(p string) bool { return strings.Contains(p, "..") }) {
			t.Errorf("kept an escaping path: %v", tree.Paths())
		}
	})

	t.Run("an oversized file is refused", func(t *testing.T) {
		big := strings.Repeat("x", 2048)
		_, err := githubapp.Extract(bytes.NewReader(tarballOf(map[string]string{"big.md": big})), commit,
			githubapp.Limits{MaxFiles: 10, MaxBytes: 1 << 20, MaxFile: 1024})
		if err == nil {
			t.Fatal("extract accepted a file over the limit")
		}
		if !strings.Contains(err.Error(), "larger than") {
			t.Errorf("error = %q, want it to name the limit", err)
		}
	})

	t.Run("too many entries is refused", func(t *testing.T) {
		many := map[string]string{}
		for i := range 20 {
			many[fmt.Sprintf("n%d.md", i)] = "x"
		}
		_, err := githubapp.Extract(bytes.NewReader(tarballOf(many)), commit,
			githubapp.Limits{MaxFiles: 5, MaxBytes: 1 << 20, MaxFile: 1 << 20})
		if err == nil {
			t.Fatal("extract accepted more entries than the limit")
		}
	})
}
