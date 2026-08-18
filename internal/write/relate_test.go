package write_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

const jellyfinPath = "services/jellyfin/dusk.md"

func relatingWriter(t *testing.T) (*write.Writer, *fakeTarget, *proof.Store) {
	t.Helper()
	return newWriter(t, located(jellyfinPath), map[string]string{
		RootPath: rootFile, jellyfinPath: jellyfinFile,
	})
}

// A file declares only edges from its own entity, which is what stops a
// repository asserting a fact about something it does not own (ADR-0026).
func TestADR0026_ARelationIsDeclaredByTheEntityItPointsFrom(t *testing.T) {
	writer, target, tokens := relatingWriter(t)

	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	result, err := writer.Relate(t.Context(), token.ID, write.Relation{
		From: jellyfin, To: "host:home/nas", Type: "runs_on",
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}

	if result.Path != jellyfinPath {
		t.Errorf("path = %q, want the file that declares %s", result.Path, jellyfin)
	}
	if result.URL == "" {
		t.Errorf("result = %+v, want somewhere a human can look", result)
	}
	if len(target.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(target.commits))
	}
	commit := target.commits[0]

	t.Run("it presents the blob it read", func(t *testing.T) {
		if commit.ReplacingSHA == "" {
			t.Error("no sha presented, so a raced write would overwrite silently")
		}
	})

	t.Run("the file that declares the target is untouched", func(t *testing.T) {
		if commit.Path == RootPath {
			t.Fatalf("the edge was written into %s, which declares host:home/nas", RootPath)
		}
		if target.files[RootPath] != rootFile {
			t.Errorf("the root file changed:\n%s", target.files[RootPath])
		}
	})

	t.Run("the file parses back with the relation", func(t *testing.T) {
		parsed, err := duskmd.ParseIncluded(jellyfinPath, commit.Content, "home", duskmd.Provenance{})
		if err != nil {
			t.Fatalf("the file Dusk wrote does not parse: %v", err)
		}
		if len(parsed.Relations) != 1 {
			t.Fatalf("relations = %v, want one", parsed.Relations)
		}

		edge := parsed.Relations[0]
		if edge.GetType() != "runs_on" || edge.GetTo() != "host:home/nas" {
			t.Errorf("relation = %s -> %s, want runs_on -> host:home/nas", edge.GetType(), edge.GetTo())
		}
		// The from side is derived by the parser rather than written, so a file
		// cannot name one at all.
		if edge.GetFrom() != jellyfin {
			t.Errorf("from = %q, want the file's own entity %q", edge.GetFrom(), jellyfin)
		}
	})

	t.Run("the prose is untouched", func(t *testing.T) {
		written := string(commit.Content)
		for _, want := range []string{"Media server.", "## Gotchas", "**Transcoding is off.**"} {
			if !strings.Contains(written, want) {
				t.Errorf("prose lost %q:\n%s", want, written)
			}
		}
	})
}

// ADR-0033: the catalog is partial by design, so a target nobody declares is
// the normal state of one still being adopted. Refusing it would make the
// correct incremental path fail.
func TestADR0033_RelateDoesNotCheckThatTheTargetExists(t *testing.T) {
	writer, target, tokens := relatingWriter(t)

	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	if _, err := writer.Relate(t.Context(), token.ID, write.Relation{
		From: jellyfin, To: "host:elsewhere/box", Type: "runs_on",
	}); err != nil {
		t.Fatalf("Relate refused a target nothing declares: %v", err)
	}
	if len(target.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(target.commits))
	}
	if !strings.Contains(string(target.commits[0].Content), "host:elsewhere/box") {
		t.Errorf("the target was not written:\n%s", target.commits[0].Content)
	}
}

// A ref the parser refuses is a different failure from one that does not
// resolve: it would be committed and then fail the whole file, taking the
// entity that declared it out of the catalog.
func TestRelateRefusesAMalformedRef(t *testing.T) {
	for _, tt := range []struct {
		name     string
		relation write.Relation
	}{
		{"no type", write.Relation{From: jellyfin, To: "host:home/nas"}},
		{"no target", write.Relation{From: jellyfin, Type: "runs_on"}},
		{"no source", write.Relation{To: "host:home/nas", Type: "runs_on"}},
		{"a target that is not a ref", write.Relation{From: jellyfin, To: "nas", Type: "runs_on"}},
		{"a target with no name", write.Relation{From: jellyfin, To: "host:home/", Type: "runs_on"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer, target, tokens := relatingWriter(t)

			token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
			if _, err := writer.Relate(t.Context(), token.ID, tt.relation); err == nil {
				t.Error("it was accepted")
			}
			if len(target.commits) != 0 {
				t.Errorf("it wrote anyway: %+v", target.commits)
			}
		})
	}
}

// ADR-0009's gate covers a relation because a relation is a change to the
// source entity's own declaration.
func TestADR0009_RelateWithoutProofIsRejected(t *testing.T) {
	for _, tt := range []struct {
		name  string
		token func(tokens *proof.Store) string
		code  string
	}{
		{"no token", func(*proof.Store) string { return "" }, proof.CodeRequired},
		{
			name: "a token from before somebody else wrote",
			token: func(tokens *proof.Store) string {
				return tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v0"}).ID
			},
			code: proof.CodeStale,
		},
		{
			name: "a token from a read that never saw it",
			token: func(tokens *proof.Store) string {
				return tokens.Issue(proof.FromGet, map[string]string{"host:home/nas": "v1"}).ID
			},
			code: proof.CodeUnseen,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer, target, tokens := relatingWriter(t)

			_, err := writer.Relate(t.Context(), tt.token(tokens), write.Relation{
				From: jellyfin, To: "host:home/nas", Type: "runs_on",
			})
			var rejection *proof.Rejection
			if !errors.As(err, &rejection) || rejection.Code != tt.code {
				t.Fatalf("err = %v, want %s", err, tt.code)
			}
			// The rejection has to name a call that reads the entity being changed.
			if !strings.Contains(rejection.Fix, `get("`+jellyfin+`")`) {
				t.Errorf("fix = %q, want the read of what the write changes", rejection.Fix)
			}
			if len(target.commits) != 0 {
				t.Errorf("a rejected relate still committed: %+v", target.commits)
			}
		})
	}
}

func TestADR0009_RelateRejectsAFileChangedBeforeReconcile(t *testing.T) {
	writer, target, tokens := relatingWriter(t)
	target.files[jellyfinPath] = strings.Replace(jellyfinFile, "Media server.", "Changed directly in Git.", 1)

	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	_, err := writer.Relate(t.Context(), token.ID, write.Relation{
		From: jellyfin, To: "host:home/nas", Type: "runs_on",
	})
	var rejection *proof.Rejection
	if !errors.As(err, &rejection) || rejection.Code != proof.CodeStale {
		t.Fatalf("err = %v, want %s", err, proof.CodeStale)
	}
	if len(target.commits) != 0 {
		t.Errorf("a stale relate still committed: %+v", target.commits)
	}
}

// A relation lives in the file of the entity it points from, so there has to be
// one. Creating it is `declare`'s job, and the answer says so.
func TestRelateRefusesASourceNothingDeclares(t *testing.T) {
	writer, target, tokens := newWriter(t, nil, map[string]string{RootPath: rootFile})

	token := tokens.Issue(proof.FromSearch, map[string]string{jellyfin: "v1"})
	_, err := writer.Relate(t.Context(), token.ID, write.Relation{
		From: jellyfin, To: "host:home/nas", Type: "runs_on",
	})
	if err == nil {
		t.Fatal("Relate wrote an edge from an entity no file declares")
	}
	if !strings.Contains(err.Error(), "Declare") {
		t.Errorf("error = %q, want it to name the call that creates the entity", err)
	}
	if len(target.commits) != 0 {
		t.Errorf("it wrote anyway: %+v", target.commits)
	}
}

// The same edge twice says nothing the first one did not, so it is answered
// rather than committed, the way an identical note is.
func TestRelateAnswersAnEdgeTheFileAlreadyDeclares(t *testing.T) {
	withRelation := strings.Replace(jellyfinFile, "title: Jellyfin\n", "title: Jellyfin\nrelations:\n  - type: runs_on\n    to: host:home/nas\n", 1)
	writer, target, tokens := newWriter(t, locatedContents(jellyfinPath, withRelation), map[string]string{
		RootPath: rootFile, jellyfinPath: withRelation,
	})

	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	again, err := writer.Relate(t.Context(), token.ID, write.Relation{
		From: jellyfin, To: "host:home/nas", Type: "runs_on",
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}
	if !again.Existing {
		t.Error("Existing = false, want the answer to say it is already declared")
	}
	if len(target.commits) != 0 {
		t.Errorf("commits = %d, want the existing edge to write nothing", len(target.commits))
	}

	t.Run("a different type to the same target is a different edge", func(t *testing.T) {
		if _, err := writer.Relate(t.Context(), token.ID, write.Relation{
			From: jellyfin, To: "host:home/nas", Type: "backs_up_to",
		}); err != nil {
			t.Fatalf("Relate: %v", err)
		}
		if len(target.commits) != 1 {
			t.Fatalf("commits = %d, want the different edge written", len(target.commits))
		}

		parsed, err := duskmd.ParseIncluded(jellyfinPath,
			target.commits[0].Content, "home", duskmd.Provenance{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(parsed.Relations) != 2 {
			t.Errorf("relations = %v, want both kept", parsed.Relations)
		}
	})
}

func TestRelateCanUpdateAndUnsetAttributes(t *testing.T) {
	withRelation := strings.Replace(jellyfinFile, "title: Jellyfin\n", "title: Jellyfin\nrelations:\n  - type: runs_on\n    to: host:home/nas\n    attributes:\n      port: old\n      protocol: https\n", 1)
	writer, target, tokens := newWriter(t, locatedContents(jellyfinPath, withRelation), map[string]string{
		RootPath: rootFile, jellyfinPath: withRelation,
	})
	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	_, err := writer.Relate(t.Context(), token.ID, write.Relation{
		From: jellyfin, To: "host:home/nas", Type: "runs_on",
		Attributes: map[string]string{"port": "8096"}, Unset: []string{"protocol"},
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}
	written := string(target.commits[0].Content)
	if !strings.Contains(written, "port: \"8096\"") || strings.Contains(written, "protocol:") {
		t.Errorf("relation attributes not updated:\n%s", written)
	}
}

func TestRelateWithdrawNeedsConfirmationAndRemovesOnlyThatEdge(t *testing.T) {
	withRelations := strings.Replace(jellyfinFile, "title: Jellyfin\n", "title: Jellyfin\nrelations:\n  - type: runs_on\n    to: host:home/nas\n  - type: backs_up_to\n    to: host:home/nas\n", 1)
	writer, target, tokens := newWriter(t, locatedContents(jellyfinPath, withRelations), map[string]string{
		RootPath: rootFile, jellyfinPath: withRelations,
	})
	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	relation := write.Relation{From: jellyfin, To: "host:home/nas", Type: "runs_on", Remove: true}
	if _, err := writer.Relate(t.Context(), token.ID, relation); err == nil {
		t.Fatal("withdraw without confirmation succeeded")
	}
	relation.Confirm = true
	if _, err := writer.Relate(t.Context(), token.ID, relation); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	written := string(target.commits[0].Content)
	if strings.Contains(written, "type: runs_on") || !strings.Contains(written, "type: backs_up_to") {
		t.Errorf("withdraw removed the wrong edges:\n%s", written)
	}
}
