package index_test

import (
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

const (
	transcoding = "Transcoding is off on purpose. Anything that will not direct play is a client problem."
	reworded    = "Transcoding is off on purpose. Anything that will not direct play is a problem with the client."
	unrelated   = "The library lives on the NAS, mounted read only so nothing can rewrite it."
)

func seedNotes(t *testing.T, db *index.DB, notes ...*duskv1alpha1.Note) {
	t.Helper()
	if err := db.Put(t.Context(), testRepo, mainRef, nil, nil, notes); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func note(id, kind, body string) *duskv1alpha1.Note {
	return &duskv1alpha1.Note{
		Id: id, Kind: kind, Body: body,
		Provenance: testProvenance(),
	}
}

func TestSimilarNotes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "the same words are the same note, whatever it is filed as",
			body: transcoding,
			want: []string{".dusk/gotcha-transcoding.md", ".dusk/todo-transcoding.md"},
		},
		{
			name: "a light edit is still the note that already exists",
			body: reworded,
			want: []string{".dusk/gotcha-transcoding.md", ".dusk/todo-transcoding.md"},
		},
		{
			name: "a note about something else is not a duplicate",
			body: "The Zigbee stick has to be on the second USB port or pairing fails.",
			want: nil,
		},
		{
			name: "nothing to compare finds nothing",
			body: "   ",
			want: nil,
		},
	}

	db := newDB(t)
	seedNotes(t, db,
		note(".dusk/gotcha-transcoding.md", "gotcha", transcoding),
		note(".dusk/todo-transcoding.md", "todo", transcoding),
		note(".dusk/gotcha-library.md", "gotcha", unrelated),
	)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alike, err := db.SimilarNotes(t.Context(), mainRef, tt.body, 5)
			if err != nil {
				t.Fatalf("SimilarNotes: %v", err)
			}

			var got []string
			for _, similar := range alike {
				got = append(got, similar.Id)
				if similar.Score < index.SimilarEnough {
					t.Errorf("%s scored %.2f, below the threshold it was returned by", similar.Id, similar.Score)
				}
			}
			if len(got) != len(tt.want) {
				t.Fatalf("similar = %v, want %v", got, tt.want)
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("similar[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

// The answer is ordered so a caller showing one shows the closest, and an
// identical body has nothing above it.
func TestSimilarNotesRanksTheClosestFirst(t *testing.T) {
	db := newDB(t)
	seedNotes(t, db,
		note(".dusk/gotcha-reworded.md", "gotcha", reworded),
		note(".dusk/gotcha-exact.md", "gotcha", transcoding),
	)

	alike, err := db.SimilarNotes(t.Context(), mainRef, transcoding, 5)
	if err != nil {
		t.Fatalf("SimilarNotes: %v", err)
	}
	if len(alike) != 2 {
		t.Fatalf("similar = %+v, want both", alike)
	}
	if alike[0].Id != ".dusk/gotcha-exact.md" {
		t.Errorf("closest = %q, want the identical body", alike[0].Id)
	}
	if alike[0].Score != 1 {
		t.Errorf("an identical body scored %.2f, want 1", alike[0].Score)
	}
	if alike[0].Kind != "gotcha" || alike[0].Body == "" {
		t.Errorf("a similar note comes back without enough to name it: %+v", alike[0])
	}
}

func TestSimilarNotesRespectsTheLimit(t *testing.T) {
	db := newDB(t)
	seedNotes(t, db,
		note(".dusk/a.md", "gotcha", transcoding),
		note(".dusk/b.md", "gotcha", transcoding),
		note(".dusk/c.md", "gotcha", reworded),
	)

	alike, err := db.SimilarNotes(t.Context(), mainRef, transcoding, 2)
	if err != nil {
		t.Fatalf("SimilarNotes: %v", err)
	}
	if len(alike) != 2 {
		t.Errorf("returned %d, want the 2 that were asked for", len(alike))
	}
}
