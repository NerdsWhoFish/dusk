package write_test

import (
	"path"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
)

const gotcha = "Transcoding is off on purpose. Anything that will not direct play is a client problem."

// noteAt is where a note lands, which ADR-0031 makes the id and ADR-0053 makes
// the duplicate check: the same body under the same kind is the same file.
func noteAt(kind, body string) string {
	return path.Join(write.NoteDir, kind+"-"+duskmd.ContentHash(body)[:8]+".md")
}

// ADR-0053: writing the same note twice answers with the one that exists, so an
// agent with no memory of the last session gets an id rather than a duplicate.
func TestADR0053_AnIdenticalNoteIsTheNoteThatExists(t *testing.T) {
	writer, target, _ := newNoteWriter(t, map[string]string{RootPath: rootFile})

	first, err := writer.Record(t.Context(), "", write.Note{Kind: "gotcha", Body: gotcha})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if !first.Created {
		t.Fatal("the first write was not a create")
	}

	second, err := writer.Record(t.Context(), "", write.Note{
		Kind: "gotcha", Body: gotcha, Refs: []string{"service:home/jellyfin"},
	})
	if err != nil {
		t.Fatalf("writing the same note again failed rather than answering: %v", err)
	}

	if !second.Existing {
		t.Error("Existing = false, so the caller reads this as a new note")
	}
	if second.Created {
		t.Error("Created = true for a note that was already there")
	}
	if second.Path != first.Path {
		t.Errorf("path = %q, want the note that already says it, %q", second.Path, first.Path)
	}
	if len(target.commits) != 1 {
		t.Errorf("commits = %d, want only the first: %+v", len(target.commits), target.commits)
	}
}

// Eight characters of hash name the file and the whole hash says it is the same
// note. Two different notes landing on one path must not overwrite each other.
func TestADifferentNoteAtTheSamePathIsRefused(t *testing.T) {
	const impostor = `---
dusk: v1alpha1
note: gotcha
---

Something else entirely.
`
	files := map[string]string{RootPath: rootFile, noteAt("gotcha", gotcha): impostor}
	writer, target, _ := newNoteWriter(t, files)

	_, err := writer.Record(t.Context(), "", write.Note{Kind: "gotcha", Body: gotcha})

	if err == nil {
		t.Fatal("a note was written over a different note at the same path")
	}
	if !strings.Contains(err.Error(), "different note") {
		t.Errorf("error = %q, want it to say what is in the way", err)
	}
	if len(target.commits) != 0 {
		t.Errorf("it committed anyway: %+v", target.commits)
	}
	if files[noteAt("gotcha", gotcha)] != impostor {
		t.Error("the note that was there is gone")
	}
}

// A near-duplicate is written and reported, never refused. Refusing would be
// wrong for a catalog somebody is still filling in, which is the same reasoning
// ADR-0033 used for a ref nothing resolves.
func TestANearDuplicateIsWrittenWithAWarning(t *testing.T) {
	writer, target, _ := newNoteWriter(t, map[string]string{RootPath: rootFile})
	writer.Catalog = fakeCatalog{alike: []index.Similarity{
		{Id: ".dusk/gotcha-9f8e7d6c.md", Kind: "gotcha", Body: gotcha, Score: 0.8},
	}}

	result, err := writer.Record(t.Context(), "", write.Note{
		Kind: "gotcha", Body: gotcha + " It has been that way since the move.",
	})
	if err != nil {
		t.Fatalf("a near-duplicate was refused: %v", err)
	}

	if !result.Created || len(target.commits) != 1 {
		t.Errorf("the note was not written: %+v", result)
	}
	if len(result.Similar) != 1 {
		t.Fatalf("similar = %+v, want the note it nearly repeats", result.Similar)
	}
	if result.Similar[0].Id != ".dusk/gotcha-9f8e7d6c.md" {
		t.Errorf("similar names %q, want the note that already says it", result.Similar[0].Id)
	}
}

// The warning is a courtesy, so an index that cannot answer costs the warning
// rather than the note.
func TestANoteIsStillWrittenWhenNothingCanBeCompared(t *testing.T) {
	writer, target, _ := newNoteWriter(t, map[string]string{RootPath: rootFile})
	writer.Catalog = nil

	result, err := writer.Record(t.Context(), "", write.Note{Kind: "gotcha", Body: gotcha})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if !result.Created || len(target.commits) != 1 {
		t.Errorf("the note was lost to a check that is only a warning: %+v", result)
	}
	if len(result.Similar) != 0 {
		t.Errorf("similar = %+v, want none", result.Similar)
	}
}
