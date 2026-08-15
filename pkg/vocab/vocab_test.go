package vocab_test

import (
	"slices"
	"testing"

	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

var minted = []vocab.Kind{
	{Namespace: vocab.Entity, Name: "service", Role: vocab.Infrastructure, Aliases: []string{"svc"}, Minted: true},
	{Namespace: vocab.Entity, Name: "host", Role: vocab.Infrastructure, Minted: true},
	{Namespace: vocab.Entity, Name: "airport", Role: vocab.Reference, Minted: true},
	{Namespace: vocab.Note, Name: "postmortem", Role: vocab.Warning, Minted: true},
}

func TestNormalizeFoldsCaseAndSeparators(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want string
	}{
		{name: "already normal", in: "service", want: "service"},
		{name: "capitalised", in: "Service", want: "service"},
		{name: "shouted", in: "SERVICE", want: "service"},
		{name: "hyphenated", in: "data-store", want: "datastore"},
		{name: "underscored", in: "data_store", want: "datastore"},
		{name: "padded", in: "  service  ", want: "service"},
		{name: "plural stays distinct", in: "services", want: "services"},
		{name: "empty", in: "  ", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := vocab.Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A mint is refused only when the name is already the vocabulary's, and Lookup
// is what answers that. Aliases resolve too, which is how `svc` stops being a
// near match and becomes a certainty.
func TestLookupResolvesNamesAndAliases(t *testing.T) {
	for _, tt := range []struct {
		name      string
		namespace vocab.Namespace
		lookup    string
		want      string
	}{
		{name: "exact", namespace: vocab.Entity, lookup: "service", want: "service"},
		{name: "different case", namespace: vocab.Entity, lookup: "Service", want: "service"},
		{name: "an alias", namespace: vocab.Entity, lookup: "svc", want: "service"},
		{name: "an alias in another case", namespace: vocab.Entity, lookup: "SVC", want: "service"},
		{name: "plural is not the same kind", namespace: vocab.Entity, lookup: "services", want: ""},
		{name: "the other namespace", namespace: vocab.Note, lookup: "service", want: ""},
		{name: "nothing", namespace: vocab.Entity, lookup: "widget", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kind, ok := vocab.Lookup(tt.namespace, tt.lookup, minted)
			found := ""
			if ok {
				found = kind.Name
			}
			if found != tt.want {
				t.Errorf("Lookup(%q) = %q, want %q", tt.lookup, found, tt.want)
			}
		})
	}
}

func TestNearestFindsTyposAndInflections(t *testing.T) {
	for _, tt := range []struct {
		name      string
		candidate string
		want      string
	}{
		{name: "plural", candidate: "services", want: "service"},
		{name: "transposed", candidate: "serivce", want: "service"},
		{name: "dropped letter", candidate: "servce", want: "service"},
		{name: "plural of a short name", candidate: "hosts", want: "host"},
		{name: "prefix", candidate: "hostname", want: "host"},
		{name: "an abbreviation is out of reach", candidate: "svc", want: ""},
		{name: "unrelated", candidate: "datastore", want: ""},
		{name: "exact is not near", candidate: "service", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			near := vocab.Nearest(vocab.Entity, tt.candidate, minted)
			if tt.want == "" {
				if len(near) > 0 {
					t.Fatalf("Nearest(%q) = %v, want nothing", tt.candidate, vocab.Names(near))
				}
				return
			}
			if len(near) == 0 || near[0].Name != tt.want {
				t.Errorf("Nearest(%q) = %v, want %q first", tt.candidate, vocab.Names(near), tt.want)
			}
		})
	}
}

// The abbreviation the matcher cannot reach is exactly what an alias is for,
// which is why one tool mints both (ADR-0048).
func TestAnAliasReachesWhatMatchingCannot(t *testing.T) {
	if near := vocab.Nearest(vocab.Entity, "svc", minted); len(near) != 0 {
		t.Errorf("Nearest(svc) = %v, want nothing: an abbreviation is four edits away", vocab.Names(near))
	}
	kind, ok := vocab.Lookup(vocab.Entity, "svc", minted)
	if !ok || kind.Name != "service" {
		t.Errorf("Lookup(svc) = %q, %v, want service", kind.Name, ok)
	}
}

func TestRoleOfPrefersMintedThenWellKnownThenDefault(t *testing.T) {
	for _, tt := range []struct {
		name      string
		namespace vocab.Namespace
		kind      string
		want      vocab.Role
	}{
		{name: "minted", namespace: vocab.Note, kind: "postmortem", want: vocab.Warning},
		{name: "well known warning", namespace: vocab.Note, kind: "gotcha", want: vocab.Warning},
		{name: "well known work", namespace: vocab.Note, kind: "todo", want: vocab.Work},
		{name: "unknown note", namespace: vocab.Note, kind: "musing", want: vocab.Knowledge},
		{name: "minted reference", namespace: vocab.Entity, kind: "airport", want: vocab.Reference},
		{name: "unknown entity", namespace: vocab.Entity, kind: "widget", want: vocab.Infrastructure},
		{name: "minted through an alias", namespace: vocab.Entity, kind: "svc", want: vocab.Infrastructure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := vocab.RoleOf(tt.namespace, tt.kind, minted); got != tt.want {
				t.Errorf("RoleOf(%s, %q) = %q, want %q", tt.namespace, tt.kind, got, tt.want)
			}
		})
	}
}

// Every seeded note kind has a role, or minting would be the only way to get
// one and the seeded set would be the decorative half.
func TestEveryWellKnownNoteKindHasARole(t *testing.T) {
	valid := vocab.Roles(vocab.Note)
	for _, kind := range vocab.WellKnown() {
		if !slices.Contains(valid, kind.Role) {
			t.Errorf("%q has role %q, want one of %v", kind.Name, kind.Role, valid)
		}
	}
	if got := vocab.Names(vocab.WithRole(vocab.WellKnown(), vocab.Work)); !slices.Equal(got, []string{"todo", "idea"}) {
		t.Errorf("work kinds = %v, want todo and idea", got)
	}
}

func TestRankPutsWarningsFirstAndWorkLast(t *testing.T) {
	if vocab.Rank(vocab.Warning) >= vocab.Rank(vocab.Knowledge) {
		t.Error("a warning does not outrank knowledge")
	}
	if vocab.Rank(vocab.Knowledge) >= vocab.Rank(vocab.Work) {
		t.Error("knowledge does not outrank work")
	}
}

func TestValidRoleRefusesTheOtherNamespacesRoles(t *testing.T) {
	for _, tt := range []struct {
		name      string
		namespace vocab.Namespace
		role      string
		wantErr   bool
	}{
		{name: "entity takes infrastructure", namespace: vocab.Entity, role: "infrastructure"},
		{name: "entity takes reference", namespace: vocab.Entity, role: "reference"},
		{name: "entity refuses warning", namespace: vocab.Entity, role: "warning", wantErr: true},
		{name: "note takes work", namespace: vocab.Note, role: "work"},
		{name: "note refuses reference", namespace: vocab.Note, role: "reference", wantErr: true},
		{name: "case is folded", namespace: vocab.Note, role: "Warning"},
		{name: "nothing is not a role", namespace: vocab.Note, role: "", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := vocab.ValidRole(tt.namespace, tt.role)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidRole(%s, %q) error = %v, want error %v", tt.namespace, tt.role, err, tt.wantErr)
			}
		})
	}
}

// A role is a judgement somebody can get wrong, so it has to be correctable.
// Correcting one must not drop what the kind is also called (ADR-0054).
func TestADR0054_MergeCorrectsAKindWithoutLosingIt(t *testing.T) {
	for _, tt := range []struct {
		name        string
		mint        vocab.Kind
		wantChanged bool
		wantRole    vocab.Role
		wantAliases []string
	}{
		{
			name:        "a new kind is added",
			mint:        vocab.Kind{Namespace: vocab.Entity, Name: "datastore", Role: vocab.Infrastructure},
			wantChanged: true,
			wantRole:    vocab.Infrastructure,
		},
		{
			name:        "a role is corrected, keeping the aliases",
			mint:        vocab.Kind{Namespace: vocab.Entity, Name: "service", Role: vocab.Reference},
			wantChanged: true,
			wantRole:    vocab.Reference,
			wantAliases: []string{"svc"},
		},
		{
			name: "an alias is added, keeping the role",
			mint: vocab.Kind{Namespace: vocab.Entity, Name: "service", Role: vocab.Infrastructure,
				Aliases: []string{"Service"}},
			wantChanged: true,
			wantRole:    vocab.Infrastructure,
			wantAliases: []string{"svc", "Service"},
		},
		{
			name: "an alias already said is not said twice",
			mint: vocab.Kind{Namespace: vocab.Entity, Name: "service", Role: vocab.Infrastructure,
				Aliases: []string{"SVC"}},
			wantRole:    vocab.Infrastructure,
			wantAliases: []string{"svc"},
		},
		{
			name:        "saying what is already said changes nothing",
			mint:        vocab.Kind{Namespace: vocab.Entity, Name: "airport", Role: vocab.Reference},
			wantRole:    vocab.Reference,
			wantAliases: nil,
		},
		{
			// The same word in the other namespace is a different kind.
			name:        "a note kind does not correct an entity kind",
			mint:        vocab.Kind{Namespace: vocab.Note, Name: "service", Role: vocab.Knowledge},
			wantChanged: true,
			wantRole:    vocab.Knowledge,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			merged, changed := vocab.Merge(minted, tt.mint)
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}

			got, ok := vocab.Lookup(tt.mint.Namespace, tt.mint.Name, merged)
			if !ok {
				t.Fatalf("the kind is not in the merged vocabulary: %+v", merged)
			}
			if got.Role != tt.wantRole {
				t.Errorf("role = %q, want %q", got.Role, tt.wantRole)
			}
			if !slices.Equal(got.Aliases, tt.wantAliases) {
				t.Errorf("aliases = %v, want %v", got.Aliases, tt.wantAliases)
			}
			if !got.Minted {
				t.Error("the merged kind does not read as minted")
			}
		})
	}

	t.Run("the vocabulary it was given is not modified", func(t *testing.T) {
		vocab.Merge(minted, vocab.Kind{Namespace: vocab.Entity, Name: "service", Role: vocab.Reference})
		if role := minted[0].Role; role != vocab.Infrastructure {
			t.Errorf("service is now %q in the original vocabulary", role)
		}
	})
}

func TestValidNameRefusesRefSeparators(t *testing.T) {
	for _, tt := range []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "plain", in: "service"},
		{name: "trimmed", in: "  service  "},
		{name: "colon", in: "ser:vice", wantErr: true},
		{name: "slash", in: "ser/vice", wantErr: true},
		{name: "space", in: "web service", wantErr: true},
		{name: "empty", in: "   ", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := vocab.ValidName(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidName(%q) error = %v, want error %v", tt.in, err, tt.wantErr)
			}
		})
	}
}
