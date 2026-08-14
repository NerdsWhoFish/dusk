package write_test

import (
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

const kindsFile = `---
dusk: v1alpha1
entities:
  - name: airport
    role: reference
---

Airports are reference data.
`

func mintingWriter(t *testing.T, files map[string]string) (*write.Writer, *fakeTarget, *proof.Store) {
	t.Helper()
	writer, target, tokens := newWriter(t, nil, files)
	writer.ConfigRepository = repo
	return writer, target, tokens
}

// The token has to come from reading the vocabulary, so a mint cannot happen
// without having seen what is already in it (ADR-0048).
func TestADR0048_MintingNeedsAVocabularyToken(t *testing.T) {
	airport := vocab.Kind{Namespace: vocab.Entity, Name: "airport", Role: vocab.Reference}

	for _, tt := range []struct {
		name   string
		origin proof.Origin
		wantOK bool
	}{
		{name: "from the vocabulary", origin: proof.FromKinds, wantOK: true},
		{name: "from a search", origin: proof.FromSearch},
		{name: "from a get", origin: proof.FromGet},
		{name: "from the page", origin: proof.FromPage},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer, _, tokens := mintingWriter(t, map[string]string{RootPath: rootFile})
			token := tokens.Issue(tt.origin, map[string]string{vocab.Path: duskmd.ContentHash("")})

			_, err := writer.MintKind(t.Context(), token.ID, airport)
			if tt.wantOK && err != nil {
				t.Fatalf("MintKind: %v", err)
			}
			if !tt.wantOK && err == nil {
				t.Errorf("a token from %s minted a kind", tt.origin)
			}
		})
	}
}

// A repository with no vocabulary file has the empty one rather than none, so
// the first mint authorizes the same way every later one does.
func TestTheFirstMintCreatesTheFile(t *testing.T) {
	writer, target, tokens := mintingWriter(t, map[string]string{RootPath: rootFile})
	token := tokens.Issue(proof.FromKinds, map[string]string{vocab.Path: duskmd.ContentHash("")})

	result, err := writer.MintKind(t.Context(), token.ID, vocab.Kind{
		Namespace: vocab.Entity, Name: "airport", Role: vocab.Reference,
		Aliases: []string{"aerodrome"},
	})
	if err != nil {
		t.Fatalf("MintKind: %v", err)
	}
	if !result.Created || result.Path != vocab.Path {
		t.Errorf("result = %+v, want a create at %s", result, vocab.Path)
	}

	written := target.files[vocab.Path]
	parsed, err := duskmd.ParseKinds(vocab.Path, []byte(written))
	if err != nil {
		t.Fatalf("the file it wrote does not parse: %v\n%s", err, written)
	}
	kind, ok := vocab.Lookup(vocab.Entity, "aerodrome", parsed.Kinds)
	if !ok || kind.Name != "airport" {
		t.Errorf("the alias did not survive the write:\n%s", written)
	}
}

// A second mint has to keep the first, and the operator's prose with it.
func TestASecondMintKeepsWhatIsThere(t *testing.T) {
	files := map[string]string{RootPath: rootFile, vocab.Path: kindsFile}
	writer, target, tokens := mintingWriter(t, files)
	token := tokens.Issue(proof.FromKinds, map[string]string{vocab.Path: duskmd.ContentHash(kindsFile)})

	_, err := writer.MintKind(t.Context(), token.ID, vocab.Kind{
		Namespace: vocab.Note, Name: "postmortem", Role: vocab.Warning,
	})
	if err != nil {
		t.Fatalf("MintKind: %v", err)
	}

	parsed, err := duskmd.ParseKinds(vocab.Path, []byte(target.files[vocab.Path]))
	if err != nil {
		t.Fatalf("ParseKinds: %v", err)
	}
	if len(parsed.Kinds) != 2 {
		t.Errorf("kinds = %d, want 2", len(parsed.Kinds))
	}
	if !strings.Contains(parsed.Body, "Airports are reference data") {
		t.Errorf("the prose was lost: %q", parsed.Body)
	}
	if commit := target.commits[len(target.commits)-1]; commit.ReplacingSHA == "" {
		t.Error("the commit did not present the blob it replaces, so a raced mint would overwrite it")
	}
}

func TestMintingRefuses(t *testing.T) {
	for _, tt := range []struct {
		name  string
		files map[string]string
		kind  vocab.Kind
		want  string
	}{
		{
			name:  "a kind the file already has",
			files: map[string]string{RootPath: rootFile, vocab.Path: kindsFile},
			kind:  vocab.Kind{Namespace: vocab.Entity, Name: "Airport", Role: vocab.Reference},
			want:  "already minted",
		},
		{
			name:  "a repository that has not opted in",
			files: map[string]string{},
			kind:  vocab.Kind{Namespace: vocab.Entity, Name: "airport", Role: vocab.Reference},
			want:  "consents",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer, _, tokens := mintingWriter(t, tt.files)
			body := tt.files[vocab.Path]
			token := tokens.Issue(proof.FromKinds, map[string]string{vocab.Path: duskmd.ContentHash(body)})

			_, err := writer.MintKind(t.Context(), token.ID, tt.kind)
			if err == nil {
				t.Fatalf("MintKind accepted %+v", tt.kind)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("MintKind error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// Without a config repository there is nowhere to keep a kind, and the reading
// half still answers rather than failing.
func TestVocabularyWithoutAConfigRepository(t *testing.T) {
	writer, _, tokens := newWriter(t, nil, map[string]string{})

	minted, err := writer.Vocabulary(t.Context())
	if err != nil {
		t.Fatalf("Vocabulary: %v", err)
	}
	if len(minted.Kinds.Kinds) != 0 || minted.Version != duskmd.ContentHash("") {
		t.Errorf("minted = %+v, want the empty vocabulary", minted)
	}

	token := tokens.Issue(proof.FromKinds, map[string]string{vocab.Path: minted.Version})
	if _, err := writer.MintKind(t.Context(), token.ID, vocab.Kind{
		Namespace: vocab.Entity, Name: "airport", Role: vocab.Reference,
	}); err == nil {
		t.Error("MintKind wrote a kind with nowhere to put it")
	}
}
