package proof_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

const jellyfin = "service:home/jellyfin"

func rejection(t *testing.T, err error) *proof.Rejection {
	t.Helper()
	if err == nil {
		t.Fatal("the write was authorized, want a rejection")
	}
	var r *proof.Rejection
	if !errors.As(err, &r) {
		t.Fatalf("error is %T, want *proof.Rejection: %v", err, err)
	}
	return r
}

// ADR-0009 exists to stop writes that never looked. A write with no token is
// the case it was written for.
func TestADR0009_WriteWithoutProofTokenIsRejected(t *testing.T) {
	store := &proof.Store{}

	t.Run("an update needs a token", func(t *testing.T) {
		r := rejection(t, store.AuthorizeUpdate("", proof.Entity(jellyfin), "v1"))
		if r.Code != proof.CodeRequired {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeRequired)
		}
	})

	t.Run("a create needs one too", func(t *testing.T) {
		r := rejection(t, store.AuthorizeCreate("", proof.Entity(jellyfin)))
		if r.Code != proof.CodeRequired {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeRequired)
		}
	})

	t.Run("an invented token id is not a token", func(t *testing.T) {
		r := rejection(t, store.AuthorizeUpdate("made-up", proof.Entity(jellyfin), "v1"))
		if r.Code != proof.CodeRequired {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeRequired)
		}
	})
}

func TestUpdate(t *testing.T) {
	store := &proof.Store{}

	t.Run("a read authorizes writing what it returned", func(t *testing.T) {
		token := store.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
		if err := store.AuthorizeUpdate(token.ID, proof.Entity(jellyfin), "v1"); err != nil {
			t.Errorf("AuthorizeUpdate: %v", err)
		}
	})

	// The version moving means somebody else wrote in between, which is the
	// collision the gate exists to catch.
	t.Run("a change since the read is rejected as stale", func(t *testing.T) {
		token := store.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
		r := rejection(t, store.AuthorizeUpdate(token.ID, proof.Entity(jellyfin), "v2"))
		if r.Code != proof.CodeStale {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeStale)
		}
	})

	t.Run("a token does not authorize what its read did not return", func(t *testing.T) {
		token := store.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
		r := rejection(t, store.AuthorizeUpdate(token.ID, proof.Entity("host:home/nas"), "v1"))
		if r.Code != proof.CodeUnseen {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeUnseen)
		}
	})

	// A token carries everything its read returned, so one search pays for a
	// working session rather than every call costing a read.
	t.Run("one read authorizes every entity it returned", func(t *testing.T) {
		token := store.Issue(proof.FromSearch, map[string]string{
			jellyfin: "v1", "host:home/nas": "v9",
		})
		for ref, version := range map[string]string{jellyfin: "v1", "host:home/nas": "v9"} {
			if err := store.AuthorizeUpdate(token.ID, proof.Entity(ref), version); err != nil {
				t.Errorf("AuthorizeUpdate(%s): %v", ref, err)
			}
		}
	})
}

// Creating requires a search that did not find it, so an agent cannot create a
// duplicate of something it never looked for.
func TestADR0009_CreatingRequiresASearchThatMissed(t *testing.T) {
	store := &proof.Store{}

	t.Run("a search that missed it authorizes creation", func(t *testing.T) {
		token := store.Issue(proof.FromSearch, map[string]string{"host:home/nas": "v9"})
		if err := store.AuthorizeCreate(token.ID, proof.Entity(jellyfin)); err != nil {
			t.Errorf("AuthorizeCreate: %v", err)
		}
	})

	// A get that found nothing proves the agent guessed one name, not that the
	// thing is absent.
	t.Run("a get cannot witness an absence", func(t *testing.T) {
		token := store.Issue(proof.FromGet, map[string]string{"host:home/nas": "v9"})
		r := rejection(t, store.AuthorizeCreate(token.ID, proof.Entity(jellyfin)))
		if r.Code != proof.CodeSearchRequired {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeSearchRequired)
		}
	})

	t.Run("a search that found it refuses to create a duplicate", func(t *testing.T) {
		token := store.Issue(proof.FromSearch, map[string]string{jellyfin: "v1"})
		r := rejection(t, store.AuthorizeCreate(token.ID, proof.Entity(jellyfin)))
		if r.Code != proof.CodeExists {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeExists)
		}
	})
}

// An unactionable error makes the contract hostile instead of teachable, so
// every rejection names the call that fixes it.
func TestEveryRejectionNamesItsFix(t *testing.T) {
	store := &proof.Store{}
	getToken := store.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	searchToken := store.Issue(proof.FromSearch, map[string]string{jellyfin: "v1"})

	for _, err := range []error{
		store.AuthorizeUpdate("", proof.Entity(jellyfin), "v1"),
		store.AuthorizeUpdate(getToken.ID, proof.Entity(jellyfin), "moved"),
		store.AuthorizeUpdate(getToken.ID, proof.Entity("host:home/nas"), "v1"),
		store.AuthorizeCreate("", proof.Entity(jellyfin)),
		store.AuthorizeCreate(getToken.ID, proof.Entity("service:home/new")),
		store.AuthorizeCreate(searchToken.ID, proof.Entity(jellyfin)),
	} {
		r := rejection(t, err)
		if r.Fix == "" {
			t.Errorf("%s carries no fix", r.Code)
		}
		if !strings.Contains(r.Error(), "call ") {
			t.Errorf("%s does not tell the agent what to call: %s", r.Code, r.Error())
		}
		if !strings.Contains(r.Error(), r.Ref) {
			t.Errorf("%s does not name the entity: %s", r.Code, r.Error())
		}
	}
}

// The rule is the read that could have witnessed the absence, and for a thing
// at a fixed path that is its own read. Requiring a search made declaring a
// first homepage impossible rather than merely awkward: search cannot name one.
func TestADR0009_ASingletonIsWitnessedAbsentByItsOwnRead(t *testing.T) {
	store := &proof.Store{}
	home := proof.Portal(".dusk/home.md")

	t.Run("its own read authorizes creating it", func(t *testing.T) {
		token := store.Issue(proof.FromPage, nil)
		if err := store.AuthorizeCreate(token.ID, home); err != nil {
			t.Errorf("AuthorizeCreate: %v", err)
		}
	})

	t.Run("a search cannot witness a file", func(t *testing.T) {
		token := store.Issue(proof.FromSearch, nil)
		r := rejection(t, store.AuthorizeCreate(token.ID, home))
		if r.Code != proof.CodeSearchRequired {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeSearchRequired)
		}
		if r.Fix != "page()" {
			t.Errorf("fix = %q, want page()", r.Fix)
		}
	})

	t.Run("a read that found it refuses to create over it", func(t *testing.T) {
		token := store.Issue(proof.FromPage, map[string]string{home.Ref: "v1"})
		r := rejection(t, store.AuthorizeCreate(token.ID, home))
		if r.Code != proof.CodeExists {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeExists)
		}
	})
}

// The fix has to be a call the surface takes. It cannot be derived from what is
// being written: a note's ref is a file path, which `get` refuses, and `kinds`
// and `page` take no ref at all.
func TestADR0009_ARejectionNamesTheCallThatReReadsIt(t *testing.T) {
	store := &proof.Store{}

	for _, tt := range []struct {
		name    string
		subject proof.Subject
		want    string
	}{
		{"an entity", proof.Entity(jellyfin), `get("service:home/jellyfin")`},
		{"a note", proof.Note(".dusk/gotcha-1a2b3c4d.md"), `note(id: ".dusk/gotcha-1a2b3c4d.md")`},
		{"the homepage", proof.Portal(".dusk/home.md"), "page()"},
		{"the context profile", proof.ContextProfile(".dusk/context.md"), "context()"},
		{"the vocabulary", proof.Vocabulary(".dusk/kinds.md"), "kinds()"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// From a read no subject here names, so the origin check refuses too.
			token := store.Issue(proof.FromNeighbors, map[string]string{tt.subject.Ref: "v1"})

			for _, err := range []error{
				store.AuthorizeUpdate("", tt.subject, "v1"),
				store.AuthorizeUpdate(token.ID, tt.subject, "moved"),
				store.AuthorizeUpdateFrom(token.ID, tt.subject, "v1"),
			} {
				if got := rejection(t, err).Fix; got != tt.want {
					t.Errorf("fix = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

// Time is the backstop, not the mechanism: tokens die when the data moves, and
// the TTL only stops an abandoned one lingering forever.
func TestTokensExpireAsABackstop(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store := &proof.Store{TTL: time.Hour, Now: func() time.Time { return now }}

	token := store.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	if err := store.AuthorizeUpdate(token.ID, proof.Entity(jellyfin), "v1"); err != nil {
		t.Fatalf("fresh token rejected: %v", err)
	}

	now = now.Add(2 * time.Hour)
	r := rejection(t, store.AuthorizeUpdate(token.ID, proof.Entity(jellyfin), "v1"))
	if r.Code != proof.CodeRequired {
		t.Errorf("code = %s, want %s once expired", r.Code, proof.CodeRequired)
	}
	if store.Lookup(token.ID) != nil {
		t.Error("an expired token is still resolvable")
	}
}

func TestTokenIDsAreUnpredictable(t *testing.T) {
	store := &proof.Store{}

	seen := make(map[string]bool, 100)
	for range 100 {
		id := store.Issue(proof.FromGet, nil).ID
		if seen[id] {
			t.Fatalf("token id %q was issued twice", id)
		}
		if len(id) < 16 {
			t.Fatalf("token id %q is too short to be unguessable", id)
		}
		seen[id] = true
	}
}
