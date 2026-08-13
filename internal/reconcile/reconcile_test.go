package reconcile_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/reconcile"
)

// Both sources satisfy the boundary, which is what ADR-0005 requires: the
// reconciler is identical over a checkout and a real repository.
var (
	_ reconcile.Source = (*reconcile.Dir)(nil)
	_ reconcile.Source = (*reconcile.Tarball)(nil)
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

const noteFile = `---
dusk: v1alpha1
note: gotcha
refs:
  - host:home/nas
  - service:home/jellyfin
---

Transcoding is off on purpose. Anything that will not direct play is a client
problem, not a server one.
`

// A repository that declares no include still has somewhere Dusk can read and
// write, or the write path would be refusable exactly where it is first tried.
func TestNotesInDuskDirNeedNoInclude(t *testing.T) {
	rootWithoutInclude := strings.Replace(rootFile, "include:\n  - services/*/dusk.md\n", "", 1)

	_, reconciler := setup(t, map[string]string{
		"dusk.md":                    rootWithoutInclude,
		".dusk/transcoding.md":       noteFile,
		".dusk/notes/second-note.md": strings.Replace(noteFile, "note: gotcha", "note: runbook", 1),
	})

	graph, err := reconciler.Reconcile(t.Context(), testRepo, mainRef, observedAt)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(graph.Notes) != 2 {
		t.Fatalf("Notes = %d, want both, one nested: %+v", len(graph.Notes), graph.Notes)
	}
	// One entity, from the root. A note must not be mistaken for one.
	if len(graph.Entities) != 1 {
		t.Errorf("Entities = %d, want only the root's", len(graph.Entities))
	}

	byKind := map[string]*duskv1alpha1.Note{}
	for _, note := range graph.Notes {
		byKind[note.GetKind()] = note
	}
	gotcha, ok := byKind["gotcha"]
	if !ok {
		t.Fatalf("no gotcha among %v", byKind)
	}

	t.Run("the path is the id", func(t *testing.T) {
		if gotcha.GetId() != ".dusk/transcoding.md" {
			t.Errorf("id = %q, want the path", gotcha.GetId())
		}
	})

	t.Run("it attaches to every ref it names", func(t *testing.T) {
		if len(gotcha.GetRefs()) != 2 {
			t.Errorf("refs = %v, want both entities", gotcha.GetRefs())
		}
	})

	t.Run("the body is the prose", func(t *testing.T) {
		if !strings.Contains(gotcha.GetBody(), "direct play") {
			t.Errorf("body = %q, want the prose", gotcha.GetBody())
		}
	})

	t.Run("identical bodies hash identically", func(t *testing.T) {
		if byKind["runbook"].GetContentHash() != gotcha.GetContentHash() {
			t.Error("the same body hashed differently, so dedup could never work")
		}
	})
}

func TestNoteFilesAreValidated(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{"a ref that is not a ref is rejected", strings.Replace(noteFile, "- host:home/nas", "- nas", 1), "kind:namespace/name"},
		{"a status nobody can close is rejected", strings.Replace(noteFile, "note: gotcha", "note: idea\nstatus: maybe", 1), "must be open, done, dropped"},
		{"a note with no prose is not a note", strings.SplitN(noteFile, "---\n\n", 2)[0] + "---\n", "has none below the frontmatter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, reconciler := setup(t, map[string]string{
				"dusk.md": rootFile, ".dusk/broken.md": tt.file,
			})
			_, err := reconciler.Reconcile(t.Context(), testRepo, mainRef, observedAt)
			if err == nil {
				t.Fatal("Reconcile accepted a broken note")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// ADR-0004 promises the root dusk.md and whatever it points at, nothing else.
// ADR-0032 narrowed that to what enters the catalog rather than what crosses
// the wire, so the assertion is on the graph rather than on the transfer.
func TestADR0004_OnlyDeclaredPathsEnterTheCatalog(t *testing.T) {
	idx, reconciler := setup(t, map[string]string{
		"dusk.md":                    rootFile,
		"services/jellyfin/dusk.md":  jellyfinFile,
		"secrets.md":                 "should never be parsed",
		"README.md":                  "should never be parsed",
		"internal/notes/private.md":  "should never be parsed",
		"services/jellyfin/notes.md": "should never be parsed",
	})

	graph, err := reconciler.Reconcile(t.Context(), testRepo, mainRef, observedAt)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	want := []string{"dusk.md", "services/jellyfin/dusk.md"}
	got := slices.Clone(graph.Files)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("parsed %v, want exactly %v", got, want)
	}

	// The undeclared files are markdown and were in the tree, so if the include
	// list were ignored they would have produced entities or parse errors.
	entities, err := idx.List(t.Context(), mainRef, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entities) != len(want) {
		t.Errorf("catalog holds %d entities, want %d", len(entities), len(want))
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

	tree, err := source.Tree(ctx, mainRef)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}

	// A tree only ever holds paths the walk produced, so an escape cannot be
	// expressed at all rather than being caught at read time.
	t.Run("a path escaping the directory is not in the tree", func(t *testing.T) {
		for _, escape := range []string{"../outside.md", "services/../../outside.md", "/etc/hosts"} {
			if _, err := tree.Read(escape); err == nil {
				t.Errorf("Read(%q) succeeded, want a refusal", escape)
			}
		}
	})

	t.Run("a missing file reports as not existing", func(t *testing.T) {
		_, err := tree.Read("absent.md")
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Read = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("another git ref is refused rather than served the same tree", func(t *testing.T) {
		if _, err := source.Tree(ctx, "refs/pull/1/head"); err == nil {
			t.Error("Tree for another ref succeeded, want a refusal")
		}
	})

	t.Run("a ref is required", func(t *testing.T) {
		if _, err := reconcile.NewDir(dir, ""); err == nil {
			t.Error("NewDir succeeded with no ref, want an error")
		}
	})
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

// An idea is often not about anything in the catalog yet, so refusing a note
// that names nothing would make the ones worth capturing the ones that cannot
// be. It is still findable: notes are read by kind and status, not only by ref.
func TestANoteAboutNothingIsStillANote(t *testing.T) {
	loose := strings.Replace(noteFile,
		"refs:\n  - host:home/nas\n  - service:home/jellyfin\n", "", 1)
	loose = strings.Replace(loose, "note: gotcha", "note: idea", 1)

	idx, reconciler := setup(t, map[string]string{
		"dusk.md": rootFile, ".dusk/paint-the-house.md": loose,
	})

	if _, err := reconciler.Reconcile(t.Context(), testRepo, mainRef, observedAt); err != nil {
		t.Fatalf("Reconcile refused a note about nothing: %v", err)
	}

	notes, err := idx.Notes(t.Context(), mainRef, index.NoteFilter{Kind: "idea"})
	if err != nil {
		t.Fatalf("read the ideas back: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("found %d ideas, want the one that was written", len(notes))
	}
}
