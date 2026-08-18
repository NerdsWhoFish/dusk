package index_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

// Provenance records the file a declaration came from and never the repository,
// so which repository owns an entity is answerable only from the column.
func TestDeclaredIsPerRepository(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, "example/one", "refs/heads/main",
		[]*duskv1alpha1.Entity{entity("service:home/a", "A", ""), entity("service:home/b", "B", "")}, nil)
	mustPut(t, db, "example/two", "refs/heads/main",
		[]*duskv1alpha1.Entity{entity("service:home/c", "C", "")}, nil)
	mustPut(t, db, index.ObservedScope("kubernetes"), "refs/heads/main",
		[]*duskv1alpha1.Entity{entity("service:home/d", "D", "")}, nil)

	tests := []struct {
		name       string
		repository string
		want       []string
	}{
		{"one repository's own", "example/one", []string{"service:home/a", "service:home/b"}},
		{"another's, not the first's", "example/two", []string{"service:home/c"}},
		{"an observation declares nothing", index.ObservedScope("kubernetes"), nil},
		{"a repository nobody has read", "example/absent", nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := db.Declared(t.Context(), "refs/heads/main", test.repository)
			if err != nil {
				t.Fatalf("Declared: %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("Declared(%q) = %v, want %v", test.repository, got, test.want)
			}
		})
	}
}

// Locate routes a write, and an observation owns no file: its repository slot
// holds an ingester scope nothing can commit to. Ordering by repository alone
// made that depend on how somebody had named a plugin.
func TestLocateRoutesToADeclarationAndNeverToAnObservation(t *testing.T) {
	// Whether the declaration or the observation sorted first was decided by the
	// repository's own name against the literal "ingester:".
	for _, repository := range []string{"alpha/homelab", "zulu/homelab"} {
		t.Run("declared in "+repository, func(t *testing.T) {
			db := newDB(t)
			for _, scope := range []string{repository, index.ObservedScope("kubernetes")} {
				mustPut(t, db, scope, mainRef,
					[]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")}, nil)
				if err := db.SetDefaultView(t.Context(), scope, mainRef); err != nil {
					t.Fatalf("SetDefaultView: %v", err)
				}
			}

			at, err := db.Locate(t.Context(), "", "service:home/jellyfin")
			if err != nil {
				t.Fatalf("Locate: %v", err)
			}
			if at.Repository != repository {
				t.Errorf("Locate routed a write at %q, want %q", at.Repository, repository)
			}
		})
	}

	// Nothing declares it, so there is no file to write to, and a create is the
	// answer rather than a commit aimed at an ingester scope.
	t.Run("observed and declared nowhere", func(t *testing.T) {
		db := newDB(t)
		scope := index.ObservedScope("kubernetes")
		mustPut(t, db, scope, mainRef,
			[]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "")}, nil)
		if err := db.SetDefaultView(t.Context(), scope, mainRef); err != nil {
			t.Fatalf("SetDefaultView: %v", err)
		}

		if _, err := db.Locate(t.Context(), "", "service:home/jellyfin"); !errors.Is(err, index.ErrNotFound) {
			t.Errorf("Locate = %v, want ErrNotFound so the write takes the create path", err)
		}
	})
}

func TestLocateReturnsTheDeclaringFileContentHash(t *testing.T) {
	db := newDB(t)
	declaration := index.Declaration{
		Path:        "jellyfin/dusk.md",
		ContentHash: "sha256-of-the-declaring-file",
		Entity:      entity("service:home/jellyfin", "Jellyfin", ""),
	}
	if err := db.Put(t.Context(), "example/homelab", mainRef, []index.Declaration{declaration}, nil, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), "example/homelab", mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	at, err := db.Locate(t.Context(), "", declaration.Entity.GetRef())
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if at.ContentHash != declaration.ContentHash {
		t.Errorf("ContentHash = %q, want %q", at.ContentHash, declaration.ContentHash)
	}
}

func TestGetFromAndLocateInSelectOneDuplicateDeclaration(t *testing.T) {
	db := newDB(t)
	ref := "service:home/jellyfin"
	for _, repository := range []string{"example/one", "example/two"} {
		declaration := index.Declaration{
			Path: repository + "/dusk.md", ContentHash: "hash-" + repository,
			Entity: entity(ref, repository, ""),
		}
		if err := db.Put(t.Context(), repository, mainRef, []index.Declaration{declaration}, nil, nil); err != nil {
			t.Fatalf("Put %s: %v", repository, err)
		}
		if err := db.SetDefaultView(t.Context(), repository, mainRef); err != nil {
			t.Fatalf("SetDefaultView %s: %v", repository, err)
		}
	}

	entity, err := db.GetFrom(t.Context(), "", ref, "example/two")
	if err != nil || entity.GetTitle() != "example/two" {
		t.Fatalf("GetFrom = %+v, %v", entity, err)
	}
	location, err := db.LocateIn(t.Context(), "", ref, "example/two")
	if err != nil || location.Repository != "example/two" || location.ContentHash != "hash-example/two" {
		t.Fatalf("LocateIn = %+v, %v", location, err)
	}
}

// A note about what a repository declares usually lives in the config
// repository, so the two halves of "notes about this repository" are stored
// apart and only the refs connect them.
func TestNotesAboutARepository(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, "example/homelab", "refs/heads/main", []*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", ""),
		entity("host:home/nas", "The NAS", ""),
	}, nil)
	mustPut(t, db, "example/other", "refs/heads/main",
		[]*duskv1alpha1.Entity{entity("service:home/paperless", "Paperless", "")}, nil)

	notes := []*duskv1alpha1.Note{
		note("both", true, "service:home/jellyfin", "host:home/nas"),
		note("one", true, "host:home/nas"),
		note("elsewhere", true, "service:home/paperless"),
		note("nothing", true),
		note("unpinned", false, "host:home/nas"),
	}
	if err := db.Put(t.Context(), "example/config", "refs/heads/main", nil, nil, notes); err != nil {
		t.Fatalf("Put: %v", err)
	}

	tests := []struct {
		name   string
		filter index.NoteFilter
		want   []string
	}{
		{
			// A note attached to two of the repository's entities is one note.
			// A join would answer with it twice.
			name:   "about what one repository declares",
			filter: index.NoteFilter{AboutRepository: "example/homelab"},
			want:   []string{".dusk/both.md", ".dusk/one.md", ".dusk/unpinned.md"},
		},
		{
			name:   "pinned and about it",
			filter: index.NoteFilter{AboutRepository: "example/homelab", Pinned: new(true)},
			want:   []string{".dusk/both.md", ".dusk/one.md"},
		},
		{
			name:   "pinned anywhere, including about nothing",
			filter: index.NoteFilter{Pinned: new(true)},
			want:   []string{".dusk/both.md", ".dusk/elsewhere.md", ".dusk/nothing.md", ".dusk/one.md"},
		},
		{
			name:   "only the unpinned",
			filter: index.NoteFilter{Pinned: new(false)},
			want:   []string{".dusk/unpinned.md"},
		},
		{
			name:   "one entity, not the repository",
			filter: index.NoteFilter{Ref: "service:home/jellyfin"},
			want:   []string{".dusk/both.md"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.filter.Limit = 50
			got, err := db.Notes(t.Context(), "refs/heads/main", test.filter)
			if err != nil {
				t.Fatalf("Notes: %v", err)
			}

			ids := make([]string, 0, len(got))
			for _, n := range got {
				ids = append(ids, n.GetId())
			}
			slices.Sort(ids)
			if !slices.Equal(ids, test.want) {
				t.Errorf("Notes = %v, want %v", ids, test.want)
			}
		})
	}
}

// A search issues the token that authorizes writing what it returned, and the
// write path compares an entity against its provenance version and a note
// against its content hash. A hit carrying neither authorizes nothing.
func TestSearchCarriesTheVersionAWriteIsCheckedAgainst(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, "example/homelab", "refs/heads/main",
		[]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "Transcoding is off.")}, nil)

	notes := []*duskv1alpha1.Note{{
		Id: ".dusk/transcoding.md", Kind: "gotcha", ContentHash: "hash-transcoding",
		Refs: []string{"service:home/jellyfin"}, Body: "Transcoding is off on purpose.",
		Provenance: testProvenance(),
	}}
	if err := db.Put(t.Context(), "example/config", "refs/heads/main", nil, nil, notes); err != nil {
		t.Fatalf("Put: %v", err)
	}

	results, _, err := db.Search(t.Context(), "refs/heads/main", index.SearchFilter{Query: "transcoding", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	want := map[string]string{
		"service:home/jellyfin": "abc123",
		".dusk/transcoding.md":  "hash-transcoding",
	}
	if len(results) != len(want) {
		t.Fatalf("Search returned %d hits, want %d", len(results), len(want))
	}
	for _, hit := range results {
		if got := hit.Version; got != want[hit.Ref] {
			t.Errorf("version of %s = %q, want %q", hit.Ref, got, want[hit.Ref])
		}
	}
}

// ADR-0059: the kind is part of the query, so a hit past the limit is still
// found. Filtering the page afterwards answers "nothing matches" whenever that
// page happens to hold none of the kind asked for.
func TestADR0059_AKindNarrowsTheQueryNotTheAnswer(t *testing.T) {
	db := newDB(t)

	entities := make([]*duskv1alpha1.Entity, 0, 21)
	for i := range 20 {
		entities = append(entities, entity(
			fmt.Sprintf("service:home/svc%02d", i), fmt.Sprintf("Service %02d", i), "shelf"))
	}
	entities = append(entities, entity("host:home/rack", "The Rack",
		"Four bays. "+strings.Repeat("It holds the media library. ", 40)+" shelf"))
	mustPut(t, db, "example/homelab", mainRef, entities, nil)

	tests := []struct {
		name  string
		kind  string
		limit int
		want  []string
	}{
		{"a kind past the limit is still found", "host", 5, []string{"host:home/rack"}},
		{"the kind is matched without regard to case", "HOST", 5, []string{"host:home/rack"}},
		{"a kind nothing carries finds nothing", "datastore", 5, nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results, _, err := db.Search(t.Context(), mainRef,
				index.SearchFilter{Query: "shelf", Kind: test.kind, Limit: test.limit})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}

			refs := make([]string, 0, len(results))
			for _, hit := range results {
				refs = append(refs, hit.Ref)
			}
			if !slices.Equal(refs, test.want) {
				t.Errorf("Search(kind=%q) = %v, want %v", test.kind, refs, test.want)
			}
		})
	}
}

// ADR-0059: a page carries how many matched, so a caller can tell a short
// answer from a complete one. The kind narrows the count with the query.
func TestADR0059_ASearchCountsWhatMatchedNotWhatFits(t *testing.T) {
	db := newDB(t)

	entities := make([]*duskv1alpha1.Entity, 0, 13)
	for i := range 12 {
		entities = append(entities, entity(
			fmt.Sprintf("service:home/svc%02d", i), fmt.Sprintf("Service %02d", i), "shelf"))
	}
	entities = append(entities, entity("host:home/rack", "The Rack", "shelf"))
	mustPut(t, db, "example/homelab", mainRef, entities, nil)

	tests := []struct {
		name      string
		filter    index.SearchFilter
		wantShown int
		wantTotal int
	}{
		{"the limit cuts the page, not the count", index.SearchFilter{Query: "shelf", Limit: 3}, 3, 13},
		{"a page that holds everything counts everything", index.SearchFilter{Query: "shelf", Limit: 50}, 13, 13},
		{"a kind narrows the count with the query", index.SearchFilter{Query: "shelf", Kind: "host", Limit: 3}, 1, 1},
		{"nothing matched counts nothing", index.SearchFilter{Query: "absent", Limit: 3}, 0, 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results, total, err := db.Search(t.Context(), mainRef, test.filter)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(results) != test.wantShown || total != test.wantTotal {
				t.Errorf("Search = %d of %d, want %d of %d",
					len(results), total, test.wantShown, test.wantTotal)
			}
		})
	}
}

// ADR-0059: one call names a list of refs. Titles resolves the way Get does, so
// a ref is named by whichever declaration Get would answer with.
func TestADR0059_TitlesNamesRefsTheWayGetResolvesThem(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, "example/homelab", mainRef, []*duskv1alpha1.Entity{
		entity("host:home/nas", "The NAS", ""),
		entity("service:home/untitled", "", ""),
	}, nil)
	mustPut(t, db, index.ObservedScope("kubernetes"), mainRef, []*duskv1alpha1.Entity{
		entity("host:home/nas", "nas-01.observed", ""),
	}, nil)

	titles, err := db.Titles(t.Context(), mainRef,
		[]string{"host:home/nas", "service:home/untitled", "service:home/absent"})
	if err != nil {
		t.Fatalf("Titles: %v", err)
	}

	want := map[string]string{"host:home/nas": "The NAS", "service:home/untitled": ""}
	if len(titles) != len(want) {
		t.Fatalf("Titles = %v, want %v", titles, want)
	}
	for ref, title := range want {
		if got, ok := titles[ref]; !ok || got != title {
			t.Errorf("Titles[%q] = %q (present %v), want %q", ref, got, ok, title)
		}
	}
}

// ADR-0060: an entity is findable by part of the name somebody gave it.
// Infrastructure names are compounds, and a full-text index matches whole words
// with a prefix on the last, so "nas" found the surname and not the host.
func TestADR0060_AnEntityIsFoundByPartOfItsName(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, "example/homelab", mainRef, []*duskv1alpha1.Entity{
		entity("host:home/backupnas", "backupnas", "Four bays."),
		entity("host:home/mediabox", "mediabox", "Plays things."),
		entity("card:board/nasty-leak", "Nasty leak in the basement", "Under the stairs."),
	}, nil)
	notes := []*duskv1alpha1.Note{{
		Id: ".dusk/todo-1.md", Kind: "todo", ContentHash: "hash-todo",
		Body: "Rebuild the nas array.", Provenance: testProvenance(),
	}}
	if err := db.Put(t.Context(), "example/config", mainRef, nil, nil, notes); err != nil {
		t.Fatalf("Put: %v", err)
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			// The word hit and the work note both come from the full-text
			// index, and the name hit sits between them.
			name:  "a part of a name is found, below a word hit and above a todo",
			query: "nas",
			want:  []string{"card:board/nasty-leak", "host:home/backupnas", ".dusk/todo-1.md"},
		},
		{
			name:  "every word has to be in the name",
			query: "media box",
			want:  []string{"host:home/mediabox"},
		},
		{
			// Two letters would be inside most of a catalog, so only the
			// prefix match answers and the compound name stays out.
			name:  "a word too short to mean anything is left to the full-text index",
			query: "na",
			want:  []string{"card:board/nasty-leak", ".dusk/todo-1.md"},
		},
		{
			name:  "a name nothing holds still finds nothing",
			query: "zzz",
			want:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results, total, err := db.Search(t.Context(), mainRef,
				index.SearchFilter{Query: test.query, Limit: 25})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}

			refs := make([]string, 0, len(results))
			for _, hit := range results {
				refs = append(refs, hit.Ref)
			}
			if !slices.Equal(refs, test.want) {
				t.Errorf("Search(%q) = %v, want %v", test.query, refs, test.want)
			}
			if total != len(test.want) {
				t.Errorf("Search(%q) total = %d, want %d", test.query, total, len(test.want))
			}
		})
	}
}

// A name hit carries the version a write is checked against, or a search would
// hand back a ref it cannot authorize writing (ADR-0009). One ref is one hit,
// however many scopes hold it, so the count ADR-0059 prints is of things.
func TestADR0060_ANameHitIsOneHitCarryingItsVersion(t *testing.T) {
	db := newDB(t)
	mustPut(t, db, "example/homelab", mainRef,
		[]*duskv1alpha1.Entity{entity("host:home/backupnas", "backupnas", "Four bays.")}, nil)
	mustPut(t, db, index.ObservedScope("kubernetes"), mainRef,
		[]*duskv1alpha1.Entity{entity("host:home/backupnas", "backupnas", "Four bays.")}, nil)

	results, total, err := db.Search(t.Context(), mainRef, index.SearchFilter{Query: "nas", Limit: 25})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || total != 1 {
		t.Fatalf("Search = %d of %d, want the one entity once", len(results), total)
	}
	if results[0].Version != "abc123" {
		t.Errorf("version = %q, want the entity's, or the hit authorizes nothing", results[0].Version)
	}
}

func note(id string, pinned bool, refs ...string) *duskv1alpha1.Note {
	return &duskv1alpha1.Note{
		Id: ".dusk/" + id + ".md", Kind: "gotcha", Body: "Something worth knowing.",
		Pinned: pinned, Refs: refs, ContentHash: "hash-" + id,
		Provenance: testProvenance(),
	}
}
