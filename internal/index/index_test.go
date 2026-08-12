package index_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/FetchHQ/dusk/internal/index"
)

const (
	mainRef  = "refs/heads/main"
	testRepo = "example/homelab"
)

func TestPutAndGet(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	want := entity("service:home/jellyfin", "Jellyfin", "Media server, transcoding disabled.")
	want.Attributes = attributes(t, map[string]any{"backup": "nightly"})

	if err := db.Put(ctx, testRepo, mainRef, declare([]*duskv1alpha1.Entity{want}), nil, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := db.Get(ctx, mainRef, "service:home/jellyfin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	for _, field := range []struct{ name, got, want string }{
		{"ref", got.GetRef(), want.GetRef()},
		{"kind", got.GetKind(), "service"},
		{"namespace", got.GetNamespace(), "home"},
		{"name", got.GetName(), "jellyfin"},
		{"title", got.GetTitle(), "Jellyfin"},
		{"description", got.GetDescription(), want.GetDescription()},
		{"provenance.source", got.GetProvenance().GetSource(), "dusk.md"},
		{"provenance.version", got.GetProvenance().GetVersion(), "abc123"},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
	if got, want := got.GetAttributes().GetFields()["backup"].GetStringValue(), "nightly"; got != want {
		t.Errorf("attributes.backup = %q, want %q", got, want)
	}
}

func TestGetReportsNotFound(t *testing.T) {
	db := newDB(t)

	_, err := db.Get(t.Context(), mainRef, "service:home/absent")
	if !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

// ADR-0008 keys the index by git ref so that several are materialized at once,
// which is what makes rendering an unmerged pull request nearly free.
func TestADR0008_GitRefsAreIsolated(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	const prRef = "refs/pull/112/head"
	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", "Media server."),
	}, nil)
	mustPut(t, db, testRepo, prRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", "Media server."),
		entity("service:home/navidrome", "Navidrome", "Music server, added by the pull request."),
	}, nil)

	t.Run("an entity added on a branch is absent from main", func(t *testing.T) {
		if _, err := db.Get(ctx, mainRef, "service:home/navidrome"); !errors.Is(err, index.ErrNotFound) {
			t.Fatalf("Get on main = %v, want ErrNotFound", err)
		}
	})

	t.Run("the same entity exists independently at both refs", func(t *testing.T) {
		for _, gitRef := range []string{mainRef, prRef} {
			if _, err := db.Get(ctx, gitRef, "service:home/jellyfin"); err != nil {
				t.Errorf("Get at %q: %v", gitRef, err)
			}
		}
	})

	t.Run("listing is scoped to one ref", func(t *testing.T) {
		onMain, err := db.List(ctx, mainRef, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(onMain) != 1 {
			t.Errorf("main holds %d entities, want 1", len(onMain))
		}
	})

	t.Run("materialized refs are enumerable", func(t *testing.T) {
		refs, err := db.GitRefs(ctx)
		if err != nil {
			t.Fatalf("GitRefs: %v", err)
		}
		if len(refs) != 2 {
			t.Errorf("GitRefs = %v, want both refs", refs)
		}
	})
}

// ADR-0008 makes garbage collecting a closed pull request a single delete
// scoped to its ref, which must not disturb any other ref.
func TestADR0008_DroppingAGitRefLeavesOthersIntact(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	const prRef = "refs/pull/112/head"
	relations := []*duskv1alpha1.Relation{
		relation("service:home/jellyfin", "host:home/nas", "runs_on"),
	}
	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "Media server.")}, relations)
	mustPut(t, db, testRepo, prRef, []*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "Media server.")}, relations)

	if err := db.DropGitRef(ctx, prRef); err != nil {
		t.Fatalf("DropGitRef: %v", err)
	}

	if _, err := db.Get(ctx, prRef, "service:home/jellyfin"); !errors.Is(err, index.ErrNotFound) {
		t.Errorf("entity survived the drop: %v", err)
	}
	if neighbors, err := db.Neighbors(ctx, prRef, "service:home/jellyfin"); err != nil || len(neighbors) != 0 {
		t.Errorf("relations survived the drop: %v, %v", neighbors, err)
	}
	if _, err := db.Get(ctx, mainRef, "service:home/jellyfin"); err != nil {
		t.Errorf("main was disturbed by dropping another ref: %v", err)
	}
	if neighbors, err := db.Neighbors(ctx, mainRef, "service:home/jellyfin"); err != nil || len(neighbors) != 1 {
		t.Errorf("main relations = %v (%v), want 1", neighbors, err)
	}
}

// Many repositories share one catalog, so the index is partitioned by
// repository as well as ref. Without that, two repositories both tracked at
// refs/heads/main would overwrite each other.
func TestRepositoriesShareARefWithoutColliding(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	const otherRepo = "example/media"
	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("host:home/nas", "The NAS", "Four bays."),
	}, []*duskv1alpha1.Relation{relation("service:home/jellyfin", "host:home/nas", "runs_on")})
	mustPut(t, db, otherRepo, mainRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", "Media server."),
	}, nil)

	t.Run("writing one repository leaves the other alone", func(t *testing.T) {
		for _, ref := range []string{"host:home/nas", "service:home/jellyfin"} {
			if _, err := db.Get(ctx, mainRef, ref); err != nil {
				t.Errorf("Get(%q): %v", ref, err)
			}
		}
	})

	t.Run("queries span every repository at the ref", func(t *testing.T) {
		all, err := db.List(ctx, mainRef, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) != 2 {
			t.Errorf("List = %d entities, want 2 across both repositories", len(all))
		}

		// The edge is declared in one repository and points at an entity owned
		// by the other, which is the normal cross-repository case.
		dependents, err := db.Dependents(ctx, mainRef, "host:home/nas", 5)
		if err != nil {
			t.Fatalf("Dependents: %v", err)
		}
		if len(dependents) != 1 {
			t.Errorf("Dependents = %v, want the service from the other repository", dependents)
		}
	})

	t.Run("re-reading one repository does not disturb the other", func(t *testing.T) {
		mustPut(t, db, otherRepo, mainRef, nil, nil)
		if _, err := db.Get(ctx, mainRef, "host:home/nas"); err != nil {
			t.Errorf("the untouched repository lost its entity: %v", err)
		}
		if _, err := db.Get(ctx, mainRef, "service:home/jellyfin"); !errors.Is(err, index.ErrNotFound) {
			t.Errorf("the re-read repository kept a removed entity: %v", err)
		}
	})

	t.Run("dropping one repository leaves the other", func(t *testing.T) {
		if err := db.DropRepository(ctx, testRepo, mainRef); err != nil {
			t.Fatalf("DropRepository: %v", err)
		}
		if _, err := db.Get(ctx, mainRef, "host:home/nas"); !errors.Is(err, index.ErrNotFound) {
			t.Errorf("the dropped repository survived: %v", err)
		}
	})
}

// Repositories disagree about what their default branch is called, so there is
// no single ref meaning "the catalog as it stands". A query with no ref has to
// span each repository's own default, or a repository on master goes missing.
func TestDefaultViewSpansEachRepositorysOwnBranch(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	const onMaster = "example/legacy"
	const masterRef = "refs/heads/master"

	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("host:home/nas", "The NAS", "Four bays."),
	}, nil)
	mustPut(t, db, onMaster, masterRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", "Media server on an older repository."),
	}, nil)

	for _, scope := range []struct{ repo, ref string }{{testRepo, mainRef}, {onMaster, masterRef}} {
		if err := db.SetDefaultView(ctx, scope.repo, scope.ref); err != nil {
			t.Fatalf("SetDefaultView: %v", err)
		}
	}

	t.Run("a query with no ref finds both", func(t *testing.T) {
		all, err := db.List(ctx, "", "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) != 2 {
			t.Errorf("List = %d entities, want both branches represented", len(all))
		}
		if _, err := db.Get(ctx, "", "service:home/jellyfin"); err != nil {
			t.Errorf("the repository on master was invisible: %v", err)
		}
	})

	t.Run("search spans them too", func(t *testing.T) {
		results, err := db.Search(ctx, "", "media", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) != 1 || results[0].Ref != "service:home/jellyfin" {
			t.Errorf("Search = %v, want the entity on master", results)
		}
	})

	t.Run("an explicit ref still reads that ref alone", func(t *testing.T) {
		onMain, err := db.List(ctx, mainRef, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(onMain) != 1 {
			t.Errorf("List(%q) = %d, want only that ref", mainRef, len(onMain))
		}
	})
}

func TestPutReplacesTheRefWholesale(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", "Media server."),
		entity("service:home/retired", "Retired", "Removed in the next commit."),
	}, nil)
	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", "Media server."),
	}, nil)

	if _, err := db.Get(ctx, mainRef, "service:home/retired"); !errors.Is(err, index.ErrNotFound) {
		t.Errorf("a removed entity survived the replace: %v", err)
	}
	remaining, err := db.List(ctx, mainRef, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("holds %d entities, want 1", len(remaining))
	}
}

// A reconcile that fails partway must leave the previous contents rather than a
// half-built graph, so the whole write is one transaction.
func TestPutRollsBackOnFailure(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", "Media server."),
	}, nil)

	duplicated := []*duskv1alpha1.Entity{
		entity("service:home/navidrome", "Navidrome", "Music server."),
		entity("service:home/navidrome", "Navidrome", "The same ref twice, which the primary key rejects."),
	}
	if err := db.Put(ctx, testRepo, mainRef, declare(duplicated), nil, nil); err == nil {
		t.Fatal("Put succeeded with a duplicate ref, want a constraint error")
	}

	if _, err := db.Get(ctx, mainRef, "service:home/jellyfin"); err != nil {
		t.Errorf("the previous contents were lost by a failed put: %v", err)
	}
	if _, err := db.Get(ctx, mainRef, "service:home/navidrome"); !errors.Is(err, index.ErrNotFound) {
		t.Errorf("a partial write survived: %v", err)
	}
}

func TestSearch(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	mustPut(t, db, testRepo, mainRef, []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", "Media server. Transcoding is disabled on purpose."),
		entity("host:home/nas", "The NAS", "Four bays, holds the media library."),
		entity("service:home/navidrome", "Navidrome", "Music streaming."),
	}, nil)

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"a word in the description matches", "transcoding", "service:home/jellyfin"},
		{"a word in the title matches", "navidrome", "service:home/navidrome"},
		{"a partial word matches as a prefix", "transcod", "service:home/jellyfin"},
		{"the kind is searchable", "host", "host:home/nas"},
		{"multiple terms narrow the result", "media library", "host:home/nas"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := db.Search(ctx, mainRef, tt.query, 10)
			if err != nil {
				t.Fatalf("Search(%q): %v", tt.query, err)
			}
			if len(results) == 0 {
				t.Fatalf("Search(%q) found nothing, want %q", tt.query, tt.want)
			}
			if results[0].Ref != tt.want {
				t.Errorf("Search(%q) top hit = %q, want %q", tt.query, results[0].Ref, tt.want)
			}
		})
	}

	t.Run("a hit carries a snippet of the match", func(t *testing.T) {
		results, err := db.Search(ctx, mainRef, "transcoding", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if !strings.Contains(results[0].Snippet, "ranscoding") {
			t.Errorf("snippet = %q, want it to quote the match", results[0].Snippet)
		}
	})

	t.Run("punctuation cannot produce a query syntax error", func(t *testing.T) {
		for _, query := range []string{`"unbalanced`, "kind:service", "a AND (b", "*", `foo"bar`} {
			if _, err := db.Search(ctx, mainRef, query, 10); err != nil {
				t.Errorf("Search(%q): %v", query, err)
			}
		}
	})

	t.Run("an empty query is rejected", func(t *testing.T) {
		if _, err := db.Search(ctx, mainRef, "   ", 10); err == nil {
			t.Error("Search succeeded with an empty query, want an error")
		}
	})
}

func TestSearchIsScopedToOneGitRef(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	mustPut(t, db, testRepo, "refs/pull/112/head", []*duskv1alpha1.Entity{
		entity("service:home/navidrome", "Navidrome", "Music streaming."),
	}, nil)

	results, err := db.Search(ctx, mainRef, "navidrome", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("found %d hits from another ref, want 0", len(results))
	}
}

func TestNeighbors(t *testing.T) {
	db := newDB(t)

	mustPut(t, db, testRepo, mainRef, nil, []*duskv1alpha1.Relation{
		relation("service:home/jellyfin", "host:home/nas", "runs_on"),
		relation("service:home/navidrome", "host:home/nas", "runs_on"),
		relation("service:home/jellyfin", "datastore:home/postgres", "depends_on"),
	})

	neighbors, err := db.Neighbors(t.Context(), mainRef, "service:home/jellyfin")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 2 {
		t.Fatalf("Neighbors = %d, want 2 (both edges out of jellyfin)", len(neighbors))
	}

	inbound, err := db.Neighbors(t.Context(), mainRef, "host:home/nas")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(inbound) != 2 {
		t.Errorf("inbound edges = %d, want 2", len(inbound))
	}
}

func TestDependents(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()

	mustPut(t, db, testRepo, mainRef, nil, []*duskv1alpha1.Relation{
		relation("service:home/jellyfin", "datastore:home/postgres", "depends_on"),
		relation("service:home/dashboard", "service:home/jellyfin", "depends_on"),
		relation("datastore:home/postgres", "host:home/nas", "runs_on"),
	})

	t.Run("traversal is transitive", func(t *testing.T) {
		dependents, err := db.Dependents(ctx, mainRef, "host:home/nas", 10)
		if err != nil {
			t.Fatalf("Dependents: %v", err)
		}
		got := map[string]int{}
		for _, d := range dependents {
			got[d.Ref] = d.Depth
		}
		want := map[string]int{
			"datastore:home/postgres": 1,
			"service:home/jellyfin":   2,
			"service:home/dashboard":  3,
		}
		if len(got) != len(want) {
			t.Fatalf("Dependents = %v, want %v", got, want)
		}
		for ref, depth := range want {
			if got[ref] != depth {
				t.Errorf("depth of %s = %d, want %d", ref, got[ref], depth)
			}
		}
	})

	t.Run("depth bounds the walk", func(t *testing.T) {
		dependents, err := db.Dependents(ctx, mainRef, "host:home/nas", 1)
		if err != nil {
			t.Fatalf("Dependents: %v", err)
		}
		if len(dependents) != 1 || dependents[0].Ref != "datastore:home/postgres" {
			t.Errorf("Dependents at depth 1 = %v, want only the direct dependent", dependents)
		}
	})

	t.Run("a depth below one is rejected", func(t *testing.T) {
		if _, err := db.Dependents(ctx, mainRef, "host:home/nas", 0); err == nil {
			t.Error("Dependents succeeded with depth 0, want an error")
		}
	})
}

// A dependency graph can contain a cycle, and a recursive walk that does not
// terminate on one would hang a request rather than fail it.
func TestDependentsTerminatesOnACycle(t *testing.T) {
	db := newDB(t)

	mustPut(t, db, testRepo, mainRef, nil, []*duskv1alpha1.Relation{
		relation("service:home/a", "service:home/b", "depends_on"),
		relation("service:home/b", "service:home/c", "depends_on"),
		relation("service:home/c", "service:home/a", "depends_on"),
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := db.Dependents(t.Context(), mainRef, "service:home/a", 50); err != nil {
			t.Errorf("Dependents: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Dependents did not terminate on a cycle")
	}
}

// declare pairs entities with a plausible declaring file, which Put now needs.
func declare(entities []*duskv1alpha1.Entity) []index.Declaration {
	out := make([]index.Declaration, 0, len(entities))
	for _, e := range entities {
		out = append(out, index.Declaration{Path: e.GetName() + "/dusk.md", Entity: e})
	}
	return out
}

func newDB(t *testing.T) *index.DB {
	t.Helper()
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustPut(t *testing.T, db *index.DB, repository, gitRef string, entities []*duskv1alpha1.Entity, relations []*duskv1alpha1.Relation) {
	t.Helper()
	if err := db.Put(t.Context(), repository, gitRef, declare(entities), relations, nil); err != nil {
		t.Fatalf("Put at %q: %v", gitRef, err)
	}
}

func entity(ref, title, description string) *duskv1alpha1.Entity {
	kind, rest, _ := strings.Cut(ref, ":")
	namespace, name, _ := strings.Cut(rest, "/")
	return &duskv1alpha1.Entity{
		Ref:         ref,
		Kind:        kind,
		Namespace:   namespace,
		Name:        name,
		Title:       title,
		Description: description,
		Provenance:  testProvenance(),
	}
}

func relation(from, to, relType string) *duskv1alpha1.Relation {
	return &duskv1alpha1.Relation{
		From:       from,
		To:         to,
		Type:       relType,
		Provenance: testProvenance(),
	}
}

func testProvenance() *duskv1alpha1.Provenance {
	return &duskv1alpha1.Provenance{
		Source:     "dusk.md",
		Version:    "abc123",
		ObservedAt: timestamppb.New(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)),
	}
}

func attributes(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	return s
}
