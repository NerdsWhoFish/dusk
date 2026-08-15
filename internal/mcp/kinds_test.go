package mcp_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// Vocabulary and MintKind back the kinds tool. The vocabulary is one file, so
// the fake keeps one in memory the way it keeps the page.
func (w *recordingWriter) Vocabulary(context.Context) (*write.Minted, error) {
	return &write.Minted{Kinds: w.vocabulary(), Version: duskmd.ContentHash(string(w.kinds))}, nil
}

func (w *recordingWriter) vocabulary() *duskmd.Kinds {
	if len(w.kinds) == 0 {
		return &duskmd.Kinds{}
	}
	parsed, err := duskmd.ParseKinds(vocab.Path, w.kinds)
	if err != nil {
		return &duskmd.Kinds{}
	}
	return parsed
}

func (w *recordingWriter) MintKind(_ context.Context, token string, kind vocab.Kind) (*write.Result, error) {
	err := w.tokens.AuthorizeUpdateFrom(token, proof.Vocabulary(vocab.Path), duskmd.ContentHash(string(w.kinds)))
	if err != nil {
		return nil, err
	}

	existing := w.vocabulary()
	kinds, changed := vocab.Merge(existing.Kinds, kind)
	if !changed {
		return &write.Result{Ref: vocab.Path, Path: vocab.Path, Existing: true}, nil
	}

	rendered, err := duskmd.FormatKinds(&duskmd.Kinds{Kinds: kinds, Body: existing.Body})
	if err != nil {
		return nil, err
	}
	w.kinds = rendered
	w.minted = append(w.minted, kind)

	return &write.Result{
		Ref: vocab.Path, Repository: w.notesGo, Path: vocab.Path,
		Commit: "c0ffee", URL: "https://github.com/example/config/commit/c0ffee",
	}, nil
}

// seedAirports puts a kind in the catalog that nobody minted, which is where
// the default role comes from.
func seedAirports(t *testing.T, idx *index.DB) {
	t.Helper()

	err := idx.Put(t.Context(), "example/travel", mainRef, []index.Declaration{
		{Path: "airports/bos.md", Entity: entity("airport:world/bos", "airport", "Boston Logan", "")},
	}, nil, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/travel", mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}

func kindsSession(t *testing.T) (*sdk.ClientSession, *recordingWriter, *index.DB) {
	t.Helper()

	idx := newIndex(t)
	seed(t, idx)

	tokens := &proof.Store{}
	writer := &recordingWriter{tokens: tokens, notesGo: "example/config"}
	server := mcp.New(mcp.Options{Catalog: idx, Tokens: tokens, Writer: writer, Version: "test"})
	return serve(t, server), writer, idx
}

func TestKindsListsWhatIsCarriedAndWhatItIsFor(t *testing.T) {
	session, _, _ := kindsSession(t)
	body := call(t, session, "kinds", nil)

	for _, want := range []string{
		"service", "host", "infrastructure",
		"gotcha", "warning", "todo", "work",
		"Proof token",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("kinds did not mention %q:\n%s", want, body)
		}
	}
}

// Reading the vocabulary is what authorizes extending it, which is why the two
// are one tool (ADR-0048).
func TestMintingNeedsTheTokenFromReadingTheVocabulary(t *testing.T) {
	session, writer, _ := kindsSession(t)

	refused := call(t, session, "kinds", map[string]any{
		"namespace": "entity", "mint": "airport", "role": "reference",
	})
	if !strings.Contains(refused, "Nothing was minted") {
		t.Errorf("a mint with no token was accepted:\n%s", refused)
	}

	fromElsewhere := tokenFrom(t, call(t, session, "search", map[string]any{"query": "jellyfin"}))
	wrongRead := call(t, session, "kinds", map[string]any{
		"namespace": "entity", "mint": "airport", "role": "reference", "proof": fromElsewhere,
	})
	if !strings.Contains(wrongRead, "Nothing was minted") {
		t.Errorf("a mint with a search token was accepted:\n%s", wrongRead)
	}

	token := tokenFrom(t, call(t, session, "kinds", nil))
	accepted := call(t, session, "kinds", map[string]any{
		"namespace": "entity", "mint": "airport", "role": "reference", "proof": token,
	})
	if !strings.Contains(accepted, "Minted") {
		t.Fatalf("a mint with the vocabulary token was refused:\n%s", accepted)
	}
	if len(writer.minted) != 1 || writer.minted[0].Name != "airport" {
		t.Errorf("minted = %+v, want one airport", writer.minted)
	}
}

// A mint says what it changed, or the role is a label and would be chosen
// carelessly.
func TestAMintSaysWhatItChanged(t *testing.T) {
	for _, tt := range []struct {
		name      string
		namespace string
		kind      string
		role      string
		want      string
	}{
		{name: "reference", namespace: "entity", kind: "airport", role: "reference", want: "Drift will stop listing"},
		{name: "warning", namespace: "note", kind: "postmortem", role: "warning", want: "surface first"},
		{name: "work", namespace: "note", kind: "chore", role: "work", want: "never outrank"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			session, _, _ := kindsSession(t)
			token := tokenFrom(t, call(t, session, "kinds", nil))
			body := call(t, session, "kinds", map[string]any{
				"namespace": tt.namespace, "mint": tt.kind, "role": tt.role, "proof": token,
			})
			if !strings.Contains(body, tt.want) {
				t.Errorf("minting %s did not say %q:\n%s", tt.kind, tt.want, body)
			}
		})
	}
}

// A kind nobody minted carries the default role, so a reference kind is
// reported by drift as infrastructure until somebody says otherwise. Refusing a
// mint of a name that exists left editing the file by hand as the only way.
func TestADR0054_ARoleCanBeCorrected(t *testing.T) {
	session, writer, idx := kindsSession(t)
	seedAirports(t, idx)

	read := call(t, session, "kinds", nil)
	if !strings.Contains(read, "airport") {
		t.Fatalf("the vocabulary does not carry the derived kind:\n%s", read)
	}

	body := call(t, session, "kinds", map[string]any{
		"namespace": "entity", "mint": "airport", "role": "reference", "proof": tokenFrom(t, read),
	})
	if strings.Contains(body, "Nothing was minted") {
		t.Fatalf("correcting a derived kind's role was refused:\n%s", body)
	}

	// The answer says what it changed, because a role is what drift acts on.
	for _, want := range []string{"infrastructure", "reference", "Drift will stop listing"} {
		if !strings.Contains(body, want) {
			t.Errorf("the answer does not say %q:\n%s", want, body)
		}
	}
	if len(writer.minted) != 1 || writer.minted[0].Role != vocab.Reference {
		t.Fatalf("minted = %+v, want airport as reference", writer.minted)
	}

	t.Run("and reading it back says so", func(t *testing.T) {
		again := call(t, session, "kinds", nil)
		if !strings.Contains(again, "airport") {
			t.Errorf("the corrected kind is not in the vocabulary:\n%s", again)
		}
	})
}

// The one refusal names an alias as the way to say it, so that call has to
// work.
func TestADR0054_TheAliasARefusalNamesCanBeAdded(t *testing.T) {
	session, writer, _ := kindsSession(t)

	refused := call(t, session, "kinds", map[string]any{
		"namespace": "entity", "mint": "Service", "role": "infrastructure",
		"proof": tokenFrom(t, call(t, session, "kinds", nil)),
	})
	if !strings.Contains(refused, "in its aliases") {
		t.Fatalf("the refusal does not name the alias:\n%s", refused)
	}

	body := call(t, session, "kinds", map[string]any{
		"namespace": "entity", "mint": "service", "role": "infrastructure",
		"aliases": []any{"Service"},
		"proof":   tokenFrom(t, call(t, session, "kinds", nil)),
	})
	if strings.Contains(body, "Nothing was minted") {
		t.Fatalf("the alias the refusal named could not be added:\n%s", body)
	}
	if len(writer.minted) != 1 || !slices.Contains(writer.minted[0].Aliases, "Service") {
		t.Errorf("minted = %+v, want the alias", writer.minted)
	}
}

// A mint that would change nothing writes nothing, rather than committing a
// file identical to the one that is there.
func TestMintingWhatIsAlreadySaidWritesNothing(t *testing.T) {
	session, writer, _ := kindsSession(t)

	mint := func() string {
		return call(t, session, "kinds", map[string]any{
			"namespace": "note", "mint": "postmortem", "role": "warning",
			"proof": tokenFrom(t, call(t, session, "kinds", nil)),
		})
	}

	if first := mint(); strings.Contains(first, "Nothing was minted") {
		t.Fatalf("the first mint was refused:\n%s", first)
	}
	if again := mint(); !strings.Contains(again, "already warning") {
		t.Errorf("the answer does not say it was already so:\n%s", again)
	}
	if len(writer.minted) != 1 {
		t.Errorf("minted = %+v, want the second to have written nothing", writer.minted)
	}
}

func TestMintingRefusesWhatIsAlreadyTheVocabularys(t *testing.T) {
	for _, tt := range []struct {
		name string
		mint string
		want string
	}{
		{name: "another spelling", mint: "Service", want: "another spelling"},
		{name: "punctuated", mint: "ser-vice", want: "another spelling"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			session, writer, _ := kindsSession(t)
			token := tokenFrom(t, call(t, session, "kinds", nil))
			body := call(t, session, "kinds", map[string]any{
				"namespace": "entity", "mint": tt.mint, "role": "infrastructure", "proof": token,
			})
			if !strings.Contains(body, tt.want) {
				t.Errorf("minting %q did not refuse with %q:\n%s", tt.mint, tt.want, body)
			}
			if len(writer.minted) != 0 {
				t.Errorf("minted = %+v, want nothing", writer.minted)
			}
		})
	}
}

func TestMintingRefusesARoleFromTheOtherNamespace(t *testing.T) {
	session, _, _ := kindsSession(t)
	token := tokenFrom(t, call(t, session, "kinds", nil))

	body := call(t, session, "kinds", map[string]any{
		"namespace": "entity", "mint": "airport", "role": "warning", "proof": token,
	})
	if !strings.Contains(body, "Nothing was minted") {
		t.Errorf("a note role on an entity kind was accepted:\n%s", body)
	}
}

// Refusing would delete ADR-0007's open vocabulary, so a near match is said
// out loud and the write still lands.
func TestADR0048_ANearMatchWarnsAndNeverRefuses(t *testing.T) {
	session, writer, _ := kindsSession(t)

	found := call(t, session, "search", map[string]any{"query": "postgres"})
	body := call(t, session, "declare", map[string]any{
		"ref": "servce:home/postgres", "proof": tokenFrom(t, found),
		"repository": "example/homelab", "title": "Postgres",
	})

	if strings.Contains(body, "The write was not made") {
		t.Fatalf("a near-matching kind was refused, and nothing may refuse one:\n%s", body)
	}
	if len(writer.got) != 1 || writer.got[0].Ref != "servce:home/postgres" {
		t.Fatalf("the declaration did not reach the writer: %+v", writer.got)
	}
	if !strings.Contains(body, "`service`") {
		t.Errorf("the answer did not name the kind it nearly matched:\n%s", body)
	}
}

// An alias turns a fuzzy guess into a certainty, which is the half of the
// feature edit distance cannot reach.
func TestAnAliasIsReportedAsOneRatherThanGuessedAt(t *testing.T) {
	session, _, idx := kindsSession(t)
	err := idx.PutVocabulary(t.Context(), "example/homelab", mainRef, []vocab.Kind{{
		Namespace: vocab.Entity, Name: "service",
		Role: vocab.Infrastructure, Aliases: []string{"svc"},
	}})
	if err != nil {
		t.Fatalf("PutVocabulary: %v", err)
	}

	found := call(t, session, "search", map[string]any{"query": "postgres"})
	body := call(t, session, "declare", map[string]any{
		"ref": "svc:home/postgres", "proof": tokenFrom(t, found),
		"repository": "example/homelab", "title": "Postgres",
	})
	if !strings.Contains(body, "is an alias of `service`") {
		t.Errorf("declaring an alias was not reported as one:\n%s", body)
	}
}

// A note's kind is not visible in its ref, which is a file path, so a result
// list without it cannot tell a gotcha from a todo (ADR-0049).
func TestSearchNamesTheKindOfANoteHit(t *testing.T) {
	session, _, idx := kindsSession(t)
	err := idx.Put(t.Context(), "example/config", mainRef, nil, nil, []*duskv1alpha1.Note{
		{Id: ".dusk/g.md", Kind: "gotcha", Body: "jellyfin transcoding is off"},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/config", mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	body := call(t, session, "search", map[string]any{"query": "transcoding"})
	if !strings.Contains(body, "gotcha · ") {
		t.Errorf("a note hit did not carry its kind:\n%s", body)
	}
}
