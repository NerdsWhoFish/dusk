package reconcile_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FetchHQ/dusk/internal/index"
	"github.com/FetchHQ/dusk/internal/reconcile"
	"github.com/FetchHQ/dusk/pkg/githubapp"
)

// Both sources satisfy the boundary, which is what ADR-0005 requires: the
// reconciler is identical over a checkout and a real repository.
var (
	_ reconcile.Source = (*reconcile.Dir)(nil)
	_ reconcile.Source = (*githubapp.Repository)(nil)
)

const (
	mainRef  = "refs/heads/main"
	testRepo = "example/homelab"
)

var observedAt = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

const rootFile = `---
dusk: v1alpha1
namespace: home
kind: host
name: nas
title: The NAS
include:
  - services/*/dusk.md
---

Four bays, holds everything.
`

const jellyfinFile = `---
dusk: v1alpha1
kind: service
name: jellyfin
title: Jellyfin
relations:
  - type: runs_on
    to: host:home/nas
---

Media server.
`

const navidromeFile = `---
dusk: v1alpha1
kind: service
name: navidrome
relations:
  - type: runs_on
    to: host:home/nas
---

Music streaming.
`

func TestReconcile(t *testing.T) {
	idx, reconciler := setup(t, map[string]string{
		"dusk.md":                     rootFile,
		"services/jellyfin/dusk.md":   jellyfinFile,
		"services/navidrome/dusk.md":  navidromeFile,
		"services/jellyfin/README.md": "not a catalog file",
	})
	ctx := t.Context()

	result, err := reconciler.Reconcile(ctx, testRepo, mainRef, observedAt)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !result.Participating {
		t.Error("Participating = false, want true")
	}
	if len(result.Entities) != 3 {
		t.Errorf("Entities = %d, want 3", len(result.Entities))
	}
	if len(result.Relations) != 2 {
		t.Errorf("Relations = %d, want 2", len(result.Relations))
	}

	t.Run("the graph is queryable", func(t *testing.T) {
		entity, err := idx.Get(ctx, mainRef, "service:home/jellyfin")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got, want := entity.GetTitle(), "Jellyfin"; got != want {
			t.Errorf("title = %q, want %q", got, want)
		}
		if got, want := entity.GetDescription(), "Media server."; got != want {
			t.Errorf("description = %q, want %q", got, want)
		}
	})

	t.Run("an included file inherits the root namespace", func(t *testing.T) {
		if _, err := idx.Get(ctx, mainRef, "service:home/navidrome"); err != nil {
			t.Errorf("Get: %v", err)
		}
	})

	t.Run("relations reached the index", func(t *testing.T) {
		dependents, err := idx.Dependents(ctx, mainRef, "host:home/nas", 5)
		if err != nil {
			t.Fatalf("Dependents: %v", err)
		}
		if len(dependents) != 2 {
			t.Errorf("dependents of the NAS = %v, want both services", dependents)
		}
	})

	t.Run("search works over the reconciled graph", func(t *testing.T) {
		results, err := idx.Search(ctx, mainRef, "streaming", 5)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) != 1 || results[0].Ref != "service:home/navidrome" {
			t.Errorf("Search = %v, want navidrome", results)
		}
	})
}

// ADR-0004 promises that Dusk reads the root dusk.md and nothing else in the
// repository unless that file explicitly points at another path. That promise
// is the whole basis for a repository consenting by containing one file.
func TestADR0004_OnlyDeclaredPathsAreRead(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"dusk.md":                    rootFile,
		"services/jellyfin/dusk.md":  jellyfinFile,
		"secrets.md":                 "should never be read",
		"README.md":                  "should never be read",
		"internal/notes/private.md":  "should never be read",
		"services/jellyfin/notes.md": "should never be read",
	})

	source, err := reconcile.NewDir(dir, mainRef)
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}
	t.Cleanup(func() { _ = source.Close() })

	recorder := &recordingSource{Source: source}
	idx := newIndex(t)
	if _, err := reconcile.New(recorder, idx).Reconcile(t.Context(), testRepo, mainRef, observedAt); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	want := []string{"dusk.md", "services/jellyfin/dusk.md"}
	slices.Sort(recorder.read)
	if !slices.Equal(recorder.read, want) {
		t.Errorf("read %v, want exactly %v", recorder.read, want)
	}
}

func TestReconcileWithoutARootFile(t *testing.T) {
	idx, reconciler := setup(t, map[string]string{
		"README.md": "a repository that has not opted in",
	})
	ctx := t.Context()

	result, err := reconciler.Reconcile(ctx, testRepo, mainRef, observedAt)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Participating {
		t.Error("Participating = true, want false for a repository with no dusk.md")
	}
	if len(result.Entities) != 0 {
		t.Errorf("Entities = %d, want 0", len(result.Entities))
	}

	t.Run("previous contents are cleared", func(t *testing.T) {
		refs, err := idx.GitRefs(ctx)
		if err != nil {
			t.Fatalf("GitRefs: %v", err)
		}
		if len(refs) != 0 {
			t.Errorf("GitRefs = %v, want none", refs)
		}
	})
}

func TestReconcileReplacesWhatWasThereBefore(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"dusk.md":                    rootFile,
		"services/jellyfin/dusk.md":  jellyfinFile,
		"services/navidrome/dusk.md": navidromeFile,
	})
	idx := newIndex(t)
	ctx := t.Context()

	reconcileDir(t, idx, dir)

	if err := os.RemoveAll(filepath.Join(dir, "services", "navidrome")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	reconcileDir(t, idx, dir)

	if _, err := idx.Get(ctx, mainRef, "service:home/navidrome"); !errors.Is(err, index.ErrNotFound) {
		t.Errorf("a removed file left its entity behind: %v", err)
	}
	if _, err := idx.Get(ctx, mainRef, "service:home/jellyfin"); err != nil {
		t.Errorf("a surviving entity was lost: %v", err)
	}
}

func TestReconcileRejectsTwoFilesDeclaringOneEntity(t *testing.T) {
	_, reconciler := setup(t, map[string]string{
		"dusk.md":                   rootFile,
		"services/jellyfin/dusk.md": jellyfinFile,
		"services/copy/dusk.md":     jellyfinFile,
	})

	_, err := reconciler.Reconcile(t.Context(), testRepo, mainRef, observedAt)
	if err == nil {
		t.Fatal("Reconcile succeeded with a duplicated entity, want an error")
	}
	for _, want := range []string{"service:home/jellyfin", "services/copy/dusk.md", "services/jellyfin/dusk.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to name %q", err, want)
		}
	}
}

func TestReconcileReportsEveryBrokenFile(t *testing.T) {
	_, reconciler := setup(t, map[string]string{
		"dusk.md":                   rootFile,
		"services/jellyfin/dusk.md": strings.Replace(jellyfinFile, "kind: service", "kidn: service", 1),
		"services/navidrome/dusk.md": strings.Replace(navidromeFile,
			"to: host:home/nas", "to: not-a-ref", 1),
	})

	_, err := reconciler.Reconcile(t.Context(), testRepo, mainRef, observedAt)
	if err == nil {
		t.Fatal("Reconcile succeeded with two broken files, want an error")
	}
	for _, want := range []string{"services/jellyfin/dusk.md", "services/navidrome/dusk.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q:\n%v", want, err)
		}
	}
}

func TestReconcileRejectsABrokenRootFile(t *testing.T) {
	_, reconciler := setup(t, map[string]string{
		"dusk.md": "no frontmatter here\n",
	})

	if _, err := reconciler.Reconcile(t.Context(), testRepo, mainRef, observedAt); err == nil {
		t.Fatal("Reconcile succeeded with a broken root file, want an error")
	}
}

func TestDir(t *testing.T) {
	dir := writeTree(t, map[string]string{"dusk.md": rootFile})
	source, err := reconcile.NewDir(dir, mainRef)
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}
	t.Cleanup(func() { _ = source.Close() })
	ctx := t.Context()

	t.Run("a path escaping the directory is refused", func(t *testing.T) {
		for _, escape := range []string{"../outside.md", "services/../../outside.md", "/etc/hosts"} {
			if _, err := source.ReadFile(ctx, mainRef, escape); err == nil {
				t.Errorf("ReadFile(%q) succeeded, want a refusal", escape)
			}
		}
	})

	t.Run("a missing file reports as not existing", func(t *testing.T) {
		_, err := source.ReadFile(ctx, mainRef, "absent.md")
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("ReadFile = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("another git ref is refused rather than served the same tree", func(t *testing.T) {
		if _, err := source.ReadFile(ctx, "refs/pull/1/head", "dusk.md"); err == nil {
			t.Error("ReadFile for another ref succeeded, want a refusal")
		}
	})

	t.Run("a ref is required", func(t *testing.T) {
		if _, err := reconcile.NewDir(dir, ""); err == nil {
			t.Error("NewDir succeeded with no ref, want an error")
		}
	})
}

type recordingSource struct {
	reconcile.Source
	read []string
}

func (r *recordingSource) ReadFile(ctx context.Context, gitRef, filePath string) ([]byte, error) {
	r.read = append(r.read, filePath)
	return r.Source.ReadFile(ctx, gitRef, filePath)
}

func setup(t *testing.T, files map[string]string) (*index.DB, *reconcile.Reconciler) {
	t.Helper()
	dir := writeTree(t, files)
	idx := newIndex(t)
	return idx, reconcile.New(newDirSource(t, dir), idx)
}

func newDirSource(t *testing.T, dir string) *reconcile.Dir {
	t.Helper()
	source, err := reconcile.NewDir(dir, mainRef)
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}
	t.Cleanup(func() { _ = source.Close() })
	return source
}

func newIndex(t *testing.T) *index.DB {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func reconcileDir(t *testing.T, idx *index.DB, dir string) {
	t.Helper()
	source := newDirSource(t, dir)
	if _, err := reconcile.New(source, idx).Reconcile(t.Context(), testRepo, mainRef, observedAt); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir for %q: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	return dir
}
