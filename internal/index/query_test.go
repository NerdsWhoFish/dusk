package index_test

import (
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
			filter: index.NoteFilter{AboutRepository: "example/homelab", Pinned: true},
			want:   []string{".dusk/both.md", ".dusk/one.md"},
		},
		{
			name:   "pinned anywhere, including about nothing",
			filter: index.NoteFilter{Pinned: true},
			want:   []string{".dusk/both.md", ".dusk/elsewhere.md", ".dusk/nothing.md", ".dusk/one.md"},
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

func note(id string, pinned bool, refs ...string) *duskv1alpha1.Note {
	return &duskv1alpha1.Note{
		Id: ".dusk/" + id + ".md", Kind: "gotcha", Body: "Something worth knowing.",
		Pinned: pinned, Refs: refs, ContentHash: "hash-" + id,
		Provenance: testProvenance(),
	}
}
