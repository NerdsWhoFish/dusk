package mcp_test

import (
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/index"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

const configRepo = "example/config"

// The note these tests replace. It is in the index and in the writer, because
// a replacement is authorized against what the note says today.
const (
	transcodingNote = ".dusk/transcoding.md"
	transcodingBody = "Transcoding is off on purpose."
)

func notingSession(t *testing.T, destination string) (*sdk.ClientSession, *recordingWriter) {
	session, writer, _ := noting(t, destination)
	return session, writer
}

// noting hands back the index too, for a test that seeds notes of its own.
func noting(t *testing.T, destination string) (*sdk.ClientSession, *recordingWriter, *index.DB) {
	t.Helper()

	idx := newIndex(t)
	seed(t, idx)
	seedNote(t, idx)

	tokens := &proof.Store{}
	writer := &recordingWriter{
		tokens: tokens, notesGo: destination,
		noteBodies: map[string]string{transcodingNote: transcodingBody},
	}

	server := mcp.New(mcp.Options{Catalog: idx, Tokens: tokens, Writer: writer, Version: "test"})
	return serve(t, server), writer, idx
}

func seedNote(t *testing.T, idx *index.DB) {
	t.Helper()

	err := idx.Put(t.Context(), configRepo, mainRef, nil, nil, []*duskv1alpha1.Note{{
		Id: transcodingNote, Kind: "gotcha", Body: transcodingBody,
		Refs:        []string{"service:home/jellyfin"},
		ContentHash: duskmd.ContentHash(transcodingBody),
	}})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), configRepo, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}

// Notes are the half of the catalog an agent writes most, so the tool has to be
// reachable without being told about it.
func TestNoteWritesWhatAnAgentLearned(t *testing.T) {
	session, writer := notingSession(t, configRepo)

	body := call(t, session, "note", map[string]any{
		"kind": "gotcha",
		"body": "Transcoding is off on purpose.",
		"refs": []any{"service:home/jellyfin", "host:home/nas"},
	})

	if len(writer.notes) != 1 {
		t.Fatalf("the surface recorded %d notes, want 1", len(writer.notes))
	}
	got := writer.notes[0]
	if got.Kind != "gotcha" {
		t.Errorf("kind = %q, want gotcha", got.Kind)
	}
	if !slices.Equal(got.Refs, []string{"service:home/jellyfin", "host:home/nas"}) {
		t.Errorf("refs = %v, want both passed through", got.Refs)
	}

	// The id is how an agent updates rather than duplicating, so the answer has
	// to hand it back and say what it is for.
	if !strings.Contains(body, ".dusk/gotcha-abcd1234.md") {
		t.Errorf("the answer does not carry the note's id:\n%s", body)
	}
	if !strings.Contains(body, "id") {
		t.Errorf("the answer does not say how to replace it:\n%s", body)
	}
}

// A deployment with nowhere to put notes still reads them: reading what has
// been written down is a read. Writing is what says where it would have gone.
func TestNoteReadsButCannotWriteWithNoConfigRepository(t *testing.T) {
	session, _ := notingSession(t, "")

	if body := call(t, session, "note", map[string]any{"kind": "idea"}); !strings.Contains(body, "No notes") {
		t.Errorf("reading should still answer:\n%s", body)
	}

	body := call(t, session, "note", map[string]any{"kind": "gotcha", "body": "x"})
	if !strings.Contains(body, "DUSK_CONFIG_REPOSITORY") {
		t.Errorf("writing should say where it would have gone:\n%s", body)
	}
}

func TestReadsMentionNoteWhenItIsAvailable(t *testing.T) {
	t.Run("with a config repository", func(t *testing.T) {
		session, _ := notingSession(t, configRepo)
		body := call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"})
		if !strings.Contains(body, "note") {
			t.Errorf("a read does not mention note, so no agent discovers it:\n%s", body)
		}
	})

	t.Run("without one", func(t *testing.T) {
		session, _ := notingSession(t, "")
		body := call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"})
		if strings.Contains(body, "`note`") {
			t.Errorf("a read offers note in a deployment that cannot write one:\n%s", body)
		}
	})
}

// A note's refs are only worth writing if the note arrives when somebody asks
// about the entity it attaches to. Without this the ref does nothing.
func TestGetReturnsTheNotesAttachedToAnEntity(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	err := idx.Put(t.Context(), "example/config", "refs/heads/main", nil, nil, []*duskv1alpha1.Note{
		{
			Id: ".dusk/transcoding.md", Kind: "gotcha",
			Refs: []string{"service:home/jellyfin"},
			Body: "Transcoding is off on purpose.",
		},
		{
			Id: ".dusk/restore.md", Kind: "runbook", Pinned: true,
			Refs: []string{"service:home/jellyfin"},
			Body: "Restoring the library from backup.",
		},
		{
			Id: ".dusk/unrelated.md", Kind: "gotcha",
			Refs: []string{"host:home/nas"},
			Body: "Nothing to do with jellyfin.",
		},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/config", "refs/heads/main"); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"})

	for _, want := range []string{"Transcoding is off on purpose.", "Restoring the library from backup."} {
		if !strings.Contains(body, want) {
			t.Errorf("get did not return an attached note:\n%s", body)
		}
	}
	if strings.Contains(body, "Nothing to do with jellyfin") {
		t.Errorf("get returned a note attached to something else:\n%s", body)
	}

	// Pinned first, so the thing worth reading first is read first.
	pinned := strings.Index(body, "Restoring the library")
	other := strings.Index(body, "Transcoding is off")
	if pinned > other {
		t.Error("the pinned note came second")
	}
}

// Replacing a note goes through the same gate a declaration does.
func TestReplacingANoteThroughTheSurfaceNeedsProof(t *testing.T) {
	session, writer := notingSession(t, configRepo)

	body := call(t, session, "note", map[string]any{
		"id":   transcodingNote,
		"body": "rewritten without having read it",
	})
	if !strings.Contains(body, "not written") {
		t.Errorf("an unproven replacement was accepted:\n%s", body)
	}
	if len(writer.notes) != 0 {
		t.Error("the write reached the writer despite being refused")
	}

	t.Run("and lands with one", func(t *testing.T) {
		token := tokenFrom(t, call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"}))
		body := call(t, session, "note", map[string]any{
			"id": transcodingNote, "proof": token, "body": "rewritten",
		})
		if strings.Contains(body, "not written") {
			t.Errorf("a proven replacement was refused:\n%s", body)
		}
	})
}

// ADR-0009 makes a token cover everything its read returned, so one search
// authorizes a session's worth of writes, and every search says so in the line
// that hands the token over.
func TestADR0009_ASearchTokenWritesTheNotesItReturned(t *testing.T) {
	session, writer := notingSession(t, configRepo)

	found := call(t, session, "search", map[string]any{"query": "transcoding"})
	if !strings.Contains(found, transcodingNote) {
		t.Fatalf("the search did not return the note:\n%s", found)
	}

	body := call(t, session, "note", map[string]any{
		"id": transcodingNote, "proof": tokenFrom(t, found), "body": "rewritten",
	})
	if strings.Contains(body, "not written") {
		t.Fatalf("a search token could not write a note that search returned:\n%s", body)
	}
	if len(writer.notes) != 1 || writer.notes[0].Body != "rewritten" {
		t.Errorf("notes = %+v, want the rewritten body", writer.notes)
	}
}
