package reconcile_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/reconcile"
)

// Dir backs `dusk validate` and Tarball backs the server. They now share one
// matcher, so what can still differ is the tree each produces: this asserts
// they agree on that, which is what keeps the local check honest.
func TestDirAndTarballProduceTheSameTree(t *testing.T) {
	files := map[string]string{
		"dusk.md":                rootFile,
		"entities/dusk.md":       jellyfinFile,
		"entities/net/switch.md": jellyfinFile,
		"docs/deep/guide.md":     "x",
		".dusk/gotcha.md":        noteFile,
		".git/config":            "not a catalog file",
		".git/objects/ab/cd.md":  "markdown, but git's",
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

	fromDir, err := local.Tree(t.Context(), commit)
	if err != nil {
		t.Fatalf("Dir.Tree: %v", err)
	}
	fromTarball, err := (&reconcile.Tarball{Repo: &fakeDownloader{files: files}}).Tree(t.Context(), commit)
	if err != nil {
		t.Fatalf("Tarball.Tree: %v", err)
	}

	if !slices.Equal(fromDir.Paths(), fromTarball.Paths()) {
		t.Errorf("the two sources disagree:\n  dir     = %v\n  tarball = %v", fromDir.Paths(), fromTarball.Paths())
	}

	want := []string{".dusk/gotcha.md", "docs/deep/guide.md", "dusk.md", "entities/dusk.md", "entities/net/switch.md"}
	if !slices.Equal(fromDir.Paths(), want) {
		t.Errorf("tree = %v, want %v", fromDir.Paths(), want)
	}
}
