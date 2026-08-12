package reconcile_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/FetchHQ/dusk/internal/reconcile"
)

// Dir backs `dusk validate` and Tarball backs the server. A pattern that
// resolves differently between them makes the local check actively misleading,
// which is worse than not having one.
func TestDirAndTarballGlobAlike(t *testing.T) {
	files := map[string]string{
		"dusk.md":                rootFile,
		"entities/dusk.md":       jellyfinFile,
		"entities/net/switch.md": jellyfinFile,
		"docs/deep/guide.md":     "x",
		".dusk/gotcha.md":        noteFile,
		".git/config":            "not a catalog file",
		"main.go":                "package main",
	}

	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	local, err := reconcile.NewDir(dir, commit)
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })

	remote := &reconcile.Tarball{Repo: &fakeDownloader{files: files}}
	if err := remote.Prepare(t.Context(), commit); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	patterns := []struct {
		pattern string
		want    []string
	}{
		// The case that shipped broken: `**` has to match no directories too,
		// or `entities/**/*.md` silently skips every file directly under it.
		{"entities/**/*.md", []string{"entities/dusk.md", "entities/net/switch.md"}},
		{"entities/*.md", []string{"entities/dusk.md"}},
		{"docs/**/*.md", []string{"docs/deep/guide.md"}},
		{"*.md", []string{"dusk.md"}},
		{"**/*.go", nil},
		{"**/config", nil},
	}

	for _, tt := range patterns {
		t.Run(tt.pattern, func(t *testing.T) {
			fromDir, err := local.Glob(t.Context(), commit, tt.pattern)
			if err != nil {
				t.Fatalf("Dir.Glob: %v", err)
			}
			fromTarball, err := remote.Glob(t.Context(), commit, tt.pattern)
			if err != nil {
				t.Fatalf("Tarball.Glob: %v", err)
			}
			if !slices.Equal(fromDir, fromTarball) {
				t.Errorf("the two sources disagree:\n  dir     = %v\n  tarball = %v", fromDir, fromTarball)
			}
			if !slices.Equal(fromDir, tt.want) {
				t.Errorf("Glob(%q) = %v, want %v", tt.pattern, fromDir, tt.want)
			}
		})
	}
}
