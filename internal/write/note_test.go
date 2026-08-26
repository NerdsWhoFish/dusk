package write_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

const noteFile = `---
dusk: v1alpha1
note: gotcha
refs:
  - service:home/jellyfin
---

Transcoding is off on purpose.
`

const pinnedNoteFile = `---
dusk: v1alpha1
note: gotcha
pinned: true
refs:
  - service:home/jellyfin
---

Transcoding is off on purpose.
`

func newNoteWriter(t *testing.T, files map[string]string) (*write.Writer, *fakeTarget, *proof.Store) {
	t.Helper()
	writer, target, tokens := newWriter(t, nil, files)
	writer.ConfigRepository = repo
	return writer, target, tokens
}

func TestANewNoteLandsInTheDuskDirectory(t *testing.T) {
	writer, target, _ := newNoteWriter(t, map[string]string{RootPath: rootFile})

	result, err := writer.Record(t.Context(), "", write.Note{
		Kind: "gotcha",
		Refs: []string{"service:home/jellyfin", "host:home/nas"},
		Body: "Transcoding is off on purpose.",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	if !strings.HasPrefix(result.Path, write.NoteDir+"/") {
		t.Errorf("note landed at %q, want it under %s/", result.Path, write.NoteDir)
	}
	if !result.Created {
		t.Error("a new note was not reported as created")
	}
	if result.Repository != repo {
		t.Errorf("note went to %q, want the config repository %q", result.Repository, repo)
	}

	t.Run("it parses back as the note that was written", func(t *testing.T) {
		parsed, err := duskmd.ParseNote(result.Path, []byte(target.files[result.Path]), duskmd.Provenance{})
		if err != nil {
			t.Fatalf("the note Dusk wrote does not parse: %v", err)
		}
		if parsed.GetKind() != "gotcha" {
			t.Errorf("kind = %q, want gotcha", parsed.GetKind())
		}
		if len(parsed.GetRefs()) != 2 {
			t.Errorf("refs = %v, want both", parsed.GetRefs())
		}
		// ADR-0031: the path is the id, so nothing else may claim to be one.
		if parsed.GetId() != result.Path {
			t.Errorf("id = %q, want the path %q", parsed.GetId(), result.Path)
		}
	})
}

// A repository consents by having a dusk.md, and the config repository is not
// exempt: a note written to one without it is committed and never read back.
func TestANoteRefusesAConfigRepositoryWithNoDuskMd(t *testing.T) {
	writer, target, _ := newNoteWriter(t, map[string]string{})

	_, err := writer.Record(t.Context(), "", write.Note{Kind: "gotcha", Body: "anything"})
	if err == nil {
		t.Fatal("a note was written to a repository that has not opted in")
	}
	if !strings.Contains(err.Error(), RootPath) {
		t.Errorf("error = %q, want it to name the missing file", err)
	}
	if len(target.commits) != 0 {
		t.Error("something was committed anyway")
	}
}

func TestANoteNeedsSomewhereToGo(t *testing.T) {
	writer, _, _ := newWriter(t, nil, map[string]string{RootPath: rootFile})

	_, err := writer.Record(t.Context(), "", write.Note{Kind: "gotcha", Body: "anything"})
	if !errors.Is(err, write.ErrNoConfigRepository) {
		t.Fatalf("err = %v, want ErrNoConfigRepository", err)
	}
}

// Replacing a note is a write over something the agent should have read, which
// is exactly what ADR-0009's gate is for.
func TestReplacingANoteNeedsProofOfHavingReadIt(t *testing.T) {
	const at = ".dusk/transcoding.md"
	writer, target, tokens := newNoteWriter(t, map[string]string{RootPath: rootFile, at: noteFile})

	t.Run("without a token it is refused", func(t *testing.T) {
		_, err := writer.Record(t.Context(), "", write.Note{Id: at, Body: "rewritten"})
		if err == nil {
			t.Fatal("a note was replaced with no proof token")
		}
		if len(target.commits) != 0 {
			t.Error("something was committed anyway")
		}
	})

	t.Run("with one it lands", func(t *testing.T) {
		hash := duskmd.ContentHash("Transcoding is off on purpose.")
		token := tokens.Issue(proof.FromGet, map[string]string{at: hash})

		result, err := writer.Record(t.Context(), token.ID, write.Note{
			Id:   at,
			Body: "Transcoding is off because the clients all direct play.",
		})
		if err != nil {
			t.Fatalf("Record: %v", err)
		}
		if result.Created {
			t.Error("replacing an existing note was reported as a create")
		}

		// Supplying only a body must not blank what the note attaches to.
		parsed, err := duskmd.ParseNote(at, []byte(target.files[at]), duskmd.Provenance{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(parsed.GetRefs()) != 1 || parsed.GetRefs()[0] != "service:home/jellyfin" {
			t.Errorf("refs = %v, want the original ones kept", parsed.GetRefs())
		}
		if parsed.GetKind() != "gotcha" {
			t.Errorf("kind = %q, want the original kept", parsed.GetKind())
		}
	})
}

// ADR-0010: an update merges, and `pinned` was the one field that could not say
// "leave it alone". Replacing a body unpinned the note, which is what
// dusk_context leads with, so an edit quietly changed every later session.
func TestADR0010_ANoteUpdateLeavesPinnedAloneUnlessItIsNamed(t *testing.T) {
	const at = ".dusk/transcoding.md"

	for _, tt := range []struct {
		name   string
		pinned *bool
		want   bool
	}{
		{"not named, so it stays as the file has it", nil, true},
		{"named false, so it is unpinned", boolPtr(false), false},
		{"named true, so it stays pinned", boolPtr(true), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer, target, tokens := newNoteWriter(t, map[string]string{
				RootPath: rootFile, at: pinnedNoteFile,
			})

			hash := duskmd.ContentHash("Transcoding is off on purpose.")
			token := tokens.Issue(proof.FromNote, map[string]string{at: hash})

			if _, err := writer.Record(t.Context(), token.ID, write.Note{
				Id:     at,
				Body:   "Transcoding is off because the clients all direct play.",
				Pinned: tt.pinned,
			}); err != nil {
				t.Fatalf("Record: %v", err)
			}

			parsed, err := duskmd.ParseNote(at, []byte(target.files[at]), duskmd.Provenance{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if parsed.GetPinned() != tt.want {
				t.Errorf("pinned = %v, want %v", parsed.GetPinned(), tt.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

// An id arrives from an agent, so it is untrusted input like any request body.
func TestANoteIdCannotEscape(t *testing.T) {
	writer, target, _ := newNoteWriter(t, map[string]string{RootPath: rootFile})

	for _, id := range []string{"../../etc/passwd.md", "/etc/passwd.md", ".dusk/../../out.md", "notes/x"} {
		t.Run(id, func(t *testing.T) {
			if _, err := writer.Record(t.Context(), "", write.Note{Id: id, Kind: "gotcha", Body: "x"}); err == nil {
				t.Errorf("id %q was accepted", id)
			}
			for _, commit := range target.commits {
				if strings.Contains(commit.Path, "..") {
					t.Errorf("committed an escaping path: %q", commit.Path)
				}
			}
		})
	}
}

func TestANoteNeedsAKindAndABody(t *testing.T) {
	writer, _, _ := newNoteWriter(t, map[string]string{RootPath: rootFile})

	tests := []struct {
		name string
		note write.Note
	}{
		{"no kind", write.Note{Body: "something worth keeping"}},
		{"no body", write.Note{Kind: "gotcha"}},
		{"blank body", write.Note{Kind: "gotcha", Body: "   \n  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := writer.Record(t.Context(), "", tt.note); err == nil {
				t.Error("the note was accepted")
			}
		})
	}
}

func TestRemovingANoteNeedsProofAndExplicitConfirmation(t *testing.T) {
	const at = ".dusk/transcoding.md"
	hash := duskmd.ContentHash("Transcoding is off on purpose.")

	t.Run("confirmation is required", func(t *testing.T) {
		writer, target, tokens := newNoteWriter(t, map[string]string{RootPath: rootFile, at: noteFile})
		token := tokens.Issue(proof.FromNote, map[string]string{at: hash})
		if _, err := writer.Record(t.Context(), token.ID, write.Note{Id: at, Remove: true}); err == nil {
			t.Fatal("the note was removed without confirmation")
		}
		if _, ok := target.files[at]; !ok {
			t.Fatal("the note disappeared after the refused removal")
		}
	})

	t.Run("a confirmed removal deletes the exact file read", func(t *testing.T) {
		writer, target, tokens := newNoteWriter(t, map[string]string{RootPath: rootFile, at: noteFile})
		token := tokens.Issue(proof.FromNote, map[string]string{at: hash})
		result, err := writer.Record(t.Context(), token.ID, write.Note{Id: at, Remove: true, Confirm: true})
		if err != nil {
			t.Fatalf("Record: %v", err)
		}
		if !result.Removed {
			t.Error("the deletion was not reported as removed")
		}
		if _, ok := target.files[at]; ok {
			t.Fatal("the note file still exists")
		}
	})
}
