package mcp_test

import (
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

// ideas seeds three notes: two ideas, one of them finished, and a gotcha, so a
// filter has something to be wrong about.
func ideas(t *testing.T, idx *index.DB) {
	t.Helper()

	notes := []*duskv1alpha1.Note{
		{
			Id: ".dusk/paint-the-house.md", Kind: "idea",
			Body:        "Paint the house black.",
			ContentHash: "hash-1",
			Provenance:  &duskv1alpha1.Provenance{Source: "dusk.md"},
		},
		{
			Id: ".dusk/rebuild-the-shed.md", Kind: "idea", Status: "done",
			Body:        "Rebuild the shed.",
			ContentHash: "hash-2",
			Provenance:  &duskv1alpha1.Provenance{Source: "dusk.md"},
		},
		{
			Id: ".dusk/transcoding.md", Kind: "gotcha",
			Refs:        []string{"service:home/jellyfin"},
			Body:        "Transcoding is off on purpose.",
			ContentHash: "hash-3",
			Provenance:  &duskv1alpha1.Provenance{Source: "dusk.md"},
		},
	}

	if err := idx.Put(t.Context(), "example/homelab", mainRef, nil, nil, notes); err != nil {
		t.Fatalf("seed notes: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/homelab", mainRef); err != nil {
		t.Fatalf("set the default view: %v", err)
	}
}

// An idea is often about nothing in the catalog yet, so refusing a note with no
// refs would make the ones worth capturing the ones that cannot be.
func TestANoteAboutNothingIsStillReadable(t *testing.T) {
	session, idx := connect(t, nil)
	ideas(t, idx)

	body := call(t, session, "note", map[string]any{"kind": "idea"})
	if !strings.Contains(body, "Paint the house black") {
		t.Fatalf("an idea attached to nothing was not returned:\n%s", body)
	}
}

func TestReadingNotesNarrowsByKindAndStatus(t *testing.T) {
	session, idx := connect(t, nil)
	ideas(t, idx)

	t.Run("by kind", func(t *testing.T) {
		body := call(t, session, "note", map[string]any{"kind": "idea"})
		if strings.Contains(body, "Transcoding") {
			t.Errorf("asking for ideas returned a gotcha:\n%s", body)
		}
	})

	t.Run("open leaves out what is finished", func(t *testing.T) {
		body := call(t, session, "note", map[string]any{"kind": "idea", "status": "open"})
		if strings.Contains(body, "Rebuild the shed") {
			t.Errorf("a finished idea came back as open:\n%s", body)
		}
		if !strings.Contains(body, "Paint the house black") {
			t.Errorf("the open idea was left out:\n%s", body)
		}
	})

	t.Run("by what it is about", func(t *testing.T) {
		body := call(t, session, "note", map[string]any{"ref": "service:home/jellyfin"})
		if !strings.Contains(body, "Transcoding") {
			t.Errorf("the note about that service was left out:\n%s", body)
		}
		if strings.Contains(body, "Paint the house") {
			t.Errorf("a note about nothing came back as being about something:\n%s", body)
		}
	})
}

// Reading is how a caller gets the token that closing one needs, which is why
// note reads and writes rather than there being a second tool.
func TestReadingNotesYieldsTheTokenThatClosesOne(t *testing.T) {
	session, _, idx := noting(t, "example/homelab")
	ideas(t, idx)

	body := call(t, session, "note", map[string]any{"kind": "idea"})
	if !strings.Contains(body, "Proof token") {
		t.Fatalf("reading notes issued no token, so nothing read can be closed:\n%s", body)
	}
}

// A closed idea is still shown, because "I already had that and dropped it" is
// the answer somebody most needs and least expects.
func TestAClosedNoteSaysSo(t *testing.T) {
	session, idx := connect(t, nil)
	ideas(t, idx)

	body := call(t, session, "note", map[string]any{"kind": "idea", "status": "done"})
	if !strings.Contains(body, "· done") {
		t.Fatalf("a finished idea did not say it was finished:\n%s", body)
	}
}

func TestAskingForNothingThatExistsSaysHowToWriteOne(t *testing.T) {
	session, idx := connect(t, nil)
	ideas(t, idx)

	body := call(t, session, "note", map[string]any{"kind": "runbook"})
	if !strings.Contains(body, "No notes") || !strings.Contains(body, "kind and a body") {
		t.Fatalf("an empty answer should say how to write one:\n%s", body)
	}
}
