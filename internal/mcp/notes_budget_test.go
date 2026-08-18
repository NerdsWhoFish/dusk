package mcp_test

import (
	"fmt"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
)

// runbooks makes n notes big enough that printing them all whole overruns the
// budget, which is the case the surface used to answer by cutting silently.
func runbooks(n int, refs ...string) []*duskv1alpha1.Note {
	notes := make([]*duskv1alpha1.Note, 0, n)
	for i := range n {
		notes = append(notes, &duskv1alpha1.Note{
			Id:          fmt.Sprintf(".dusk/runbook-%02d.md", i),
			Kind:        "runbook",
			Body:        fmt.Sprintf("Runbook %02d.\n%s", i, strings.Repeat("Steps go here. ", 60)),
			ContentHash: fmt.Sprintf("hash-%02d", i),
			Refs:        refs,
			Provenance:  &duskv1alpha1.Provenance{Source: ".dusk", Version: "abc123"},
		})
	}
	return notes
}

func putNotes(t *testing.T, idx *index.DB, notes []*duskv1alpha1.Note) {
	t.Helper()
	if err := idx.Put(t.Context(), "example/config", mainRef, nil, nil, notes); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/config", mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}

// ADR-0059: a note list says how many matched. It answered with ten of
// thirty-five runbooks and nothing saying so, which reads as thirty-five
// existing and having been seen.
func TestADR0059_ANoteListSaysHowManyItIsNotShowing(t *testing.T) {
	session, idx := connect(t, nil)
	putNotes(t, idx, runbooks(35))

	t.Run("every match is accounted for", func(t *testing.T) {
		body := call(t, session, "note", map[string]any{"kind": "runbook"})
		if !strings.Contains(body, "35 note(s)") {
			t.Errorf("note body does not say how many matched:\n%s", first(body))
		}
		for _, id := range []string{".dusk/runbook-00.md", ".dusk/runbook-34.md"} {
			if !strings.Contains(body, id) {
				t.Errorf("note %s is neither printed nor named:\n%s", id, first(body))
			}
		}
	})

	t.Run("a limit the caller asked for says what it left out", func(t *testing.T) {
		body := call(t, session, "note", map[string]any{"kind": "runbook", "limit": 5})
		if !strings.Contains(body, "1-5 of 35 note(s)") {
			t.Errorf("note body does not say what the limit cut:\n%s", first(body))
		}
		if !strings.Contains(body, "`offset` 5") {
			t.Errorf("note body does not say how to see the rest:\n%s", first(body))
		}
	})

	t.Run("the offset it names answers with the next page", func(t *testing.T) {
		body := call(t, session, "note", map[string]any{"kind": "runbook", "limit": 5, "offset": 5})
		if !strings.Contains(body, "6-10 of 35 note(s)") {
			t.Errorf("note body does not report the page it answered with:\n%s", first(body))
		}
	})
}

// A filter the tool accepts and drops is worse than one it rejects: the rows
// come back ordered pinned-first, so a caller reading a page of them concludes
// the whole catalog is pinned. That misreading is what this test exists for.
func TestNoteReadFiltersOnPinned(t *testing.T) {
	session, idx := connect(t, nil)
	notes := runbooks(6)
	notes[0].Pinned = true
	notes[1].Pinned = true
	putNotes(t, idx, notes)

	t.Run("only the pinned", func(t *testing.T) {
		body := call(t, session, "note", map[string]any{"kind": "runbook", "pinned": true})
		if !strings.Contains(body, "2 note(s)") {
			t.Errorf("pinned read did not narrow to the pinned:\n%s", first(body))
		}
	})

	t.Run("only the unpinned", func(t *testing.T) {
		body := call(t, session, "note", map[string]any{"kind": "runbook", "pinned": false})
		if !strings.Contains(body, "4 note(s)") {
			t.Errorf("unpinned read did not narrow to the unpinned:\n%s", first(body))
		}
	})

	t.Run("left out is every note", func(t *testing.T) {
		body := call(t, session, "note", map[string]any{"kind": "runbook"})
		if !strings.Contains(body, "6 note(s)") {
			t.Errorf("an unfiltered read should answer with every note:\n%s", first(body))
		}
	})
}

// ADR-0059: what does not fit is named, never dropped. Ten runbooks printed
// whole were 67,836 characters, so the bound is bytes rather than notes.
func TestADR0059_NotesTooLargeToPrintAreNamed(t *testing.T) {
	session, idx := connect(t, nil)
	seed(t, idx)
	putNotes(t, idx, runbooks(35, "service:home/jellyfin"))

	body := call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"})

	if len(body) > 4*mcp.NotesBudget {
		t.Errorf("get is %d bytes, want it bounded near the %d byte note budget",
			len(body), mcp.NotesBudget)
	}
	if !strings.Contains(body, "35 note(s)") {
		t.Errorf("get does not say how many notes are attached:\n%s", first(body))
	}
	if !strings.Contains(body, "printed whole") {
		t.Errorf("get does not say that it named rather than printed some:\n%s", first(body))
	}

	// Named, not dropped: an agent that has read the line knows what to ask for.
	for i := range 35 {
		id := fmt.Sprintf(".dusk/runbook-%02d.md", i)
		if !strings.Contains(body, id) {
			t.Fatalf("note %s vanished from get rather than being named:\n%s", id, first(body))
		}
	}
}

// ADR-0059 keeps a titles-only mode for an agent that wants to know what is
// attached without reading any of it, and does not make it the size fix.
func TestADR0059_GetCanNameNotesWithoutPrintingThem(t *testing.T) {
	session, idx := connect(t, nil)
	seed(t, idx)
	putNotes(t, idx, runbooks(3, "service:home/jellyfin"))

	body := call(t, session, "get", map[string]any{"ref": "service:home/jellyfin", "titles": true})

	if strings.Contains(body, "Steps go here") {
		t.Errorf("titles printed a note body:\n%s", first(body))
	}
	for i := range 3 {
		if id := fmt.Sprintf(".dusk/runbook-%02d.md", i); !strings.Contains(body, id) {
			t.Errorf("titles left out %s:\n%s", id, first(body))
		}
	}
	if !strings.Contains(body, "note") {
		t.Errorf("titles does not say which call returns one whole:\n%s", first(body))
	}
}

// first keeps a failure message readable when the body under test is the size
// the test exists to bound.
func first(body string) string {
	if len(body) <= 1200 {
		return body
	}
	return body[:1200] + "\n..."
}
