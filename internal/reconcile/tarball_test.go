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

func (f *fakeDownloader) Tarball(_ context.Context, at string, limits githubapp.Limits) (*githubapp.Tree, error) {
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

	if err := source.Prepare(t.Context(), commit); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	t.Run("every file comes from the one download", func(t *testing.T) {
		for _, name := range []string{"dusk.md", "services/jellyfin/dusk.md", ".dusk/transcoding.md"} {
			if _, err := source.ReadFile(t.Context(), commit, name); err != nil {
				t.Errorf("ReadFile(%q): %v", name, err)
			}
		}
		if downloader.downloads != 1 {
			t.Errorf("downloaded %d times, want 1", downloader.downloads)
		}
	})

	t.Run("non-markdown is not carried", func(t *testing.T) {
		if _, err := source.ReadFile(t.Context(), commit, "main.go"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("main.go was kept, which is weight for nothing")
		}
	})

	t.Run("globbing is local", func(t *testing.T) {
		matches, err := source.Glob(t.Context(), commit, "services/*/dusk.md")
		if err != nil {
			t.Fatalf("Glob: %v", err)
		}
		if !slices.Equal(matches, []string{"services/jellyfin/dusk.md"}) {
			t.Errorf("Glob = %v, want the one service", matches)
		}
	})
}

// A repository with no dusk.md has not opted in, so nothing about it is
// transferred. It is the overwhelming majority of any installation.
func TestARepositoryWithoutDuskMdIsNeverDownloaded(t *testing.T) {
	downloader := &fakeDownloader{noRootFile: true}
	source := &reconcile.Tarball{Repo: downloader}

	err := source.Prepare(t.Context(), commit)
	if !errors.Is(err, reconcile.ErrNotParticipating) {
		t.Fatalf("Prepare = %v, want ErrNotParticipating", err)
	}
	if downloader.downloads != 0 {
		t.Errorf("downloaded a repository that has not opted in")
	}
	if downloader.probed != 1 {
		t.Errorf("probed %d times, want exactly 1", downloader.probed)
	}
}

// `**` is what a documentation tree needs, and path.Match has no such thing.
func TestGlobHandlesRecursion(t *testing.T) {
	source := &reconcile.Tarball{Repo: &fakeDownloader{files: map[string]string{
		"dusk.md":                rootFile,
		"docs/a.md":              "x",
		"docs/deep/b.md":         "x",
		"docs/deeper/still/c.md": "x",
		"elsewhere/d.md":         "x",
		".dusk/notes/gotcha.md":  "x",
	}}}
	if err := source.Prepare(t.Context(), commit); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	tests := []struct {
		pattern string
		want    []string
	}{
		{"docs/**/*.md", []string{"docs/a.md", "docs/deep/b.md", "docs/deeper/still/c.md"}},
		{"docs/*.md", []string{"docs/a.md"}},
		{"**/*.md", nil},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			matches, err := source.Glob(t.Context(), commit, tt.pattern)
			if err != nil {
				t.Fatalf("Glob: %v", err)
			}
			if tt.want == nil {
				if len(matches) == 0 {
					t.Error("a bare ** matched nothing")
				}
				return
			}
			if !slices.Equal(matches, tt.want) {
				t.Errorf("Glob(%q) = %v, want %v", tt.pattern, matches, tt.want)
			}
		})
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
		for _, name := range tree.Files() {
			if strings.Contains(name, "..") {
				t.Errorf("kept an escaping path: %q", name)
			}
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

func TestReadingBeforePrepareIsAnError(t *testing.T) {
	source := &reconcile.Tarball{Repo: &fakeDownloader{}}
	if _, err := source.ReadFile(t.Context(), commit, "dusk.md"); err == nil {
		t.Error("ReadFile succeeded with nothing downloaded")
	}
}
