package mcp_test

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// dedupingWriter answers the way the write path does when a note is already
// there, or nearly is.
type dedupingWriter struct {
	*recordingWriter
	existing string
	alike    []index.Similarity
}

func (w *dedupingWriter) Record(ctx context.Context, token string, n write.Note) (*write.Result, error) {
	result, err := w.recordingWriter.Record(ctx, token, n)
	if err != nil {
		return nil, err
	}
	if w.existing != "" {
		return &write.Result{
			Ref: w.existing, Repository: configRepo, Path: w.existing, Existing: true,
		}, nil
	}
	result.Similar = w.alike
	return result, nil
}

func dedupingSession(t *testing.T, writer *dedupingWriter) *sdk.ClientSession {
	t.Helper()

	idx := newIndex(t)
	seed(t, idx)

	tokens := &proof.Store{}
	writer.recordingWriter = &recordingWriter{tokens: tokens, notesGo: configRepo}

	return serve(t, mcp.New(mcp.Options{Catalog: idx, Tokens: tokens, Writer: writer, Version: "test"}))
}

// An agent with no memory of a previous session cannot know an id, so writing
// the same note again has to answer with one rather than with a second note.
func TestANoteAlreadyWrittenComesBackAsItsId(t *testing.T) {
	const already = ".dusk/gotcha-1a2b3c4d.md"
	session := dedupingSession(t, &dedupingWriter{existing: already})

	body := call(t, session, "note", map[string]any{
		"kind": "gotcha", "body": "Transcoding is off on purpose.",
	})

	for _, want := range []string{"already there", already, "`id`"} {
		if !strings.Contains(body, want) {
			t.Errorf("the answer is missing %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"Commit:", "Wrote the note"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the answer claims a write (%q):\n%s", unwanted, body)
		}
	}
}

// A near-duplicate is written and reported. Refusing it would make the catalog
// argue with somebody who is still filling it in.
func TestANearDuplicateIsWrittenAndNamed(t *testing.T) {
	session := dedupingSession(t, &dedupingWriter{alike: []index.Similarity{{
		Id:    ".dusk/gotcha-9f8e7d6c.md",
		Kind:  "gotcha",
		Body:  "Transcoding is off on purpose. Anything that will not direct play is a client problem.",
		Score: 0.8,
	}}})

	body := call(t, session, "note", map[string]any{
		"kind": "gotcha", "body": "Transcoding is off on purpose, and has been since the move.",
	})

	if !strings.Contains(body, "Wrote the note") {
		t.Errorf("the note was not written:\n%s", body)
	}
	for _, want := range []string{"close to this", ".dusk/gotcha-9f8e7d6c.md", "direct play"} {
		if !strings.Contains(body, want) {
			t.Errorf("the warning is missing %q:\n%s", want, body)
		}
	}
}
