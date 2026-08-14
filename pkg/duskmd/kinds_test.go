package duskmd_test

import (
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

const kindsFile = `---
dusk: v1alpha1
entities:
  - name: airport
    role: reference
    aliases:
      - aerodrome
  - name: service
    role: infrastructure
notes:
  - name: postmortem
    role: warning
---

Airports are reference data. Nobody is going to declare Boston Logan.
`

func TestParseKinds(t *testing.T) {
	kinds, err := duskmd.ParseKinds(vocab.Path, []byte(kindsFile))
	if err != nil {
		t.Fatalf("ParseKinds: %v", err)
	}

	if len(kinds.Kinds) != 3 {
		t.Fatalf("Kinds = %d, want 3", len(kinds.Kinds))
	}
	if !strings.HasPrefix(kinds.Body, "Airports are reference data") {
		t.Errorf("Body = %q, want the prose below the frontmatter", kinds.Body)
	}

	airport, ok := vocab.Lookup(vocab.Entity, "airport", kinds.Kinds)
	if !ok {
		t.Fatal("airport is not in the parsed vocabulary")
	}
	if airport.Role != vocab.Reference {
		t.Errorf("airport role = %q, want reference", airport.Role)
	}
	if !airport.Minted {
		t.Error("a parsed kind is not marked minted")
	}
	if _, ok := vocab.Lookup(vocab.Entity, "aerodrome", kinds.Kinds); !ok {
		t.Error("the declared alias does not resolve")
	}
	if _, ok := vocab.Lookup(vocab.Note, "airport", kinds.Kinds); ok {
		t.Error("an entity kind resolved in the note namespace")
	}
}

func TestParseKindsRejects(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "no frontmatter",
			body: "just prose\n",
			want: "frontmatter",
		},
		{
			name: "wrong schema version",
			body: "---\ndusk: v2\nnotes:\n  - name: x\n    role: work\n---\n",
			want: "dusk",
		},
		{
			name: "an unknown top level field",
			body: "---\ndusk: v1alpha1\nrelations: []\n---\n",
			want: "relations",
		},
		{
			name: "an unknown field on a kind",
			body: "---\ndusk: v1alpha1\nnotes:\n  - name: x\n    role: work\n    weight: 3\n---\n",
			want: "weight",
		},
		{
			name: "a role from the other namespace",
			body: "---\ndusk: v1alpha1\nentities:\n  - name: x\n    role: warning\n---\n",
			want: "role",
		},
		{
			name: "no role at all",
			body: "---\ndusk: v1alpha1\nentities:\n  - name: x\n---\n",
			want: "role",
		},
		{
			name: "a name carrying a ref separator",
			body: "---\ndusk: v1alpha1\nentities:\n  - name: a/b\n    role: reference\n---\n",
			want: "name",
		},
		{
			name: "the same kind twice",
			body: "---\ndusk: v1alpha1\nentities:\n  - name: service\n    role: reference\n  - name: Service\n    role: reference\n---\n",
			want: "already minted",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := duskmd.ParseKinds(vocab.Path, []byte(tt.body))
			if err == nil {
				t.Fatalf("ParseKinds accepted %q", tt.body)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseKinds error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// A rewrite has to round-trip, or minting a second kind would rewrite the
// first one into something the parser reads differently.
func TestFormatKindsRoundTrips(t *testing.T) {
	parsed, err := duskmd.ParseKinds(vocab.Path, []byte(kindsFile))
	if err != nil {
		t.Fatalf("ParseKinds: %v", err)
	}

	rendered, err := duskmd.FormatKinds(parsed)
	if err != nil {
		t.Fatalf("FormatKinds: %v", err)
	}

	again, err := duskmd.ParseKinds(vocab.Path, rendered)
	if err != nil {
		t.Fatalf("ParseKinds of the rendered file: %v\n%s", err, rendered)
	}
	if len(again.Kinds) != len(parsed.Kinds) {
		t.Errorf("Kinds = %d, want %d", len(again.Kinds), len(parsed.Kinds))
	}
	if again.Body != parsed.Body {
		t.Errorf("Body = %q, want %q", again.Body, parsed.Body)
	}

	twice, err := duskmd.FormatKinds(again)
	if err != nil {
		t.Fatalf("FormatKinds again: %v", err)
	}
	if string(twice) != string(rendered) {
		t.Errorf("formatting is not stable:\n%s\nwant:\n%s", twice, rendered)
	}
}

func TestFormatKindsRefusesAnEmptyVocabulary(t *testing.T) {
	if _, err := duskmd.FormatKinds(&duskmd.Kinds{Body: "nothing"}); err == nil {
		t.Error("FormatKinds accepted a file that would mint nothing")
	}
}

// The seeded set and the closable set come from one list, so a kind cannot be
// well known and role-less at the same time.
func TestWellKnownNoteKindsComeFromTheVocabulary(t *testing.T) {
	if len(duskmd.WellKnownNoteKinds) != len(vocab.WellKnown()) {
		t.Errorf("WellKnownNoteKinds = %v, want one per seeded kind", duskmd.WellKnownNoteKinds)
	}
	for _, working := range duskmd.Working {
		if vocab.RoleOf(vocab.Note, working, nil) != vocab.Work {
			t.Errorf("%q is closable and is not work", working)
		}
	}
}
