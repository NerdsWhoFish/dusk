package index_test

import (
	"slices"
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

func note(id string, pinned bool, refs ...string) *duskv1alpha1.Note {
	return &duskv1alpha1.Note{
		Id: ".dusk/" + id + ".md", Kind: "gotcha", Body: "Something worth knowing.",
		Pinned: pinned, Refs: refs, ContentHash: "hash-" + id,
		Provenance: testProvenance(),
	}
}
