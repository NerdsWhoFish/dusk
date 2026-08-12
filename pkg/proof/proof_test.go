package proof_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FetchHQ/dusk/pkg/proof"
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
		r := rejection(t, store.AuthorizeUpdate("", jellyfin, "v1"))
		if r.Code != proof.CodeRequired {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeRequired)
		}
	})

	t.Run("a create needs one too", func(t *testing.T) {
		r := rejection(t, store.AuthorizeCreate("", jellyfin))
		if r.Code != proof.CodeRequired {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeRequired)
		}
	})

	t.Run("an invented token id is not a token", func(t *testing.T) {
		r := rejection(t, store.AuthorizeUpdate("made-up", jellyfin, "v1"))
		if r.Code != proof.CodeRequired {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeRequired)
		}
	})
}

func TestUpdate(t *testing.T) {
	store := &proof.Store{}

	t.Run("a read authorizes writing what it returned", func(t *testing.T) {
		token := store.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
		if err := store.AuthorizeUpdate(token.ID, jellyfin, "v1"); err != nil {
			t.Errorf("AuthorizeUpdate: %v", err)
		}
	})

	// The version moving means somebody else wrote in between, which is the
	// collision the gate exists to catch.
	t.Run("a change since the read is rejected as stale", func(t *testing.T) {
		token := store.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
		r := rejection(t, store.AuthorizeUpdate(token.ID, jellyfin, "v2"))
		if r.Code != proof.CodeStale {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeStale)
		}
	})

	t.Run("a token does not authorize what its read did not return", func(t *testing.T) {
		token := store.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
		r := rejection(t, store.AuthorizeUpdate(token.ID, "host:home/nas", "v1"))
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
			if err := store.AuthorizeUpdate(token.ID, ref, version); err != nil {
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
		if err := store.AuthorizeCreate(token.ID, jellyfin); err != nil {
			t.Errorf("AuthorizeCreate: %v", err)
		}
	})

	// A get that found nothing proves the agent guessed one name, not that the
	// thing is absent.
	t.Run("a get cannot witness an absence", func(t *testing.T) {
		token := store.Issue(proof.FromGet, map[string]string{"host:home/nas": "v9"})
		r := rejection(t, store.AuthorizeCreate(token.ID, jellyfin))
		if r.Code != proof.CodeSearchRequired {
			t.Errorf("code = %s, want %s", r.Code, proof.CodeSearchRequired)
		}
	})

	t.Run("a search that found it refuses to create a duplicate", func(t *testing.T) {
		token := store.Issue(proof.FromSearch, map[string]string{jellyfin: "v1"})
		r := rejection(t, store.AuthorizeCreate(token.ID, jellyfin))
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
		store.AuthorizeUpdate("", jellyfin, "v1"),
		store.AuthorizeUpdate(getToken.ID, jellyfin, "moved"),
		store.AuthorizeUpdate(getToken.ID, "host:home/nas", "v1"),
		store.AuthorizeCreate("", jellyfin),
		store.AuthorizeCreate(getToken.ID, "service:home/new"),
		store.AuthorizeCreate(searchToken.ID, jellyfin),
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

// Time is the backstop, not the mechanism: tokens die when the data moves, and
// the TTL only stops an abandoned one lingering forever.
func TestTokensExpireAsABackstop(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store := &proof.Store{TTL: time.Hour, Now: func() time.Time { return now }}

	token := store.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	if err := store.AuthorizeUpdate(token.ID, jellyfin, "v1"); err != nil {
		t.Fatalf("fresh token rejected: %v", err)
	}

	now = now.Add(2 * time.Hour)
	r := rejection(t, store.AuthorizeUpdate(token.ID, jellyfin, "v1"))
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
