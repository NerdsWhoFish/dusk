// Package proof implements the read-before-write gate from ADR-0009.
//
// Every agent write presents a token issued by a read. The token carries what
// that read returned, and a write is accepted only if its target was in there
// and has not changed since.
//
// One invariant replaces three mechanisms: it is optimistic concurrency, it is
// duplicate prevention, and it is the vocabulary gate.
package proof

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// Origin is the read that issued a token. It matters because only an
// enumerating read can witness an absence: a get that found nothing proves the
// agent guessed one name.
type Origin string

const (
	// FromSearch enumerates, so it can witness an absence.
	FromSearch Origin = "search"
	// FromGet resolves one ref and cannot.
	FromGet Origin = "get"
	// FromNeighbors resolves around one ref and cannot either.
	FromNeighbors Origin = "neighbors"

	// FromPage is reading the portal page, which is the only read that
	// witnesses the whole of what a write would replace.
	FromPage Origin = "page"

	// FromKinds is reading the vocabulary. Extending one you have not read is
	// how `svc` gets invented next to `service` (ADR-0048).
	FromKinds Origin = "kinds"
)

// DefaultTTL is a backstop, not the mechanism. Tokens are invalidated by the
// data changing; time only stops an abandoned one lingering forever.
const DefaultTTL = time.Hour

// Token is proof that an agent read before writing.
type Token struct {
	ID       string
	Origin   Origin
	IssuedAt time.Time

	// seen maps each entity ref the issuing read returned to its version at
	// that moment.
	seen map[string]string
}

// Covers reports whether ref was in the read that issued this token.
func (t *Token) Covers(ref string) bool {
	_, ok := t.seen[ref]
	return ok
}

// Store issues and validates tokens.
type Store struct {
	// TTL defaults to DefaultTTL.
	TTL time.Duration
	// Now exists so tests can age a token out without waiting.
	Now func() time.Time

	mu     sync.Mutex
	tokens map[string]*Token
}

// Issue mints a token for what a read returned, keyed by entity ref to the
// version that read saw.
func (s *Store) Issue(origin Origin, seen map[string]string) *Token {
	token := &Token{
		ID:       newID(),
		Origin:   origin,
		IssuedAt: s.now(),
		seen:     make(map[string]string, len(seen)),
	}
	for ref, version := range seen {
		token.seen[ref] = version
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = make(map[string]*Token)
	}
	s.expireLocked()
	s.tokens[token.ID] = token
	return token
}

// Lookup returns a live token, or nil when it is unknown or expired.
func (s *Store) Lookup(id string) *Token {
	s.mu.Lock()
	defer s.mu.Unlock()

	token, ok := s.tokens[id]
	if !ok || s.staleLocked(token) {
		return nil
	}
	return token
}

// AuthorizeUpdate accepts a write to a ref that already exists. A token
// recording a version other than currentVersion means somebody wrote in
// between, which is the collision this gate exists to catch.
func (s *Store) AuthorizeUpdate(tokenID, ref, currentVersion string) error {
	token := s.Lookup(tokenID)
	if token == nil {
		return required(ref)
	}

	seenVersion, covered := token.seen[ref]
	if !covered {
		return &Rejection{
			Code:   CodeUnseen,
			Ref:    ref,
			Detail: "the token presented came from a read that did not return this entity",
			Fix:    fmt.Sprintf(`get(%q)`, ref),
		}
	}
	if seenVersion != currentVersion {
		return &Rejection{
			Code:   CodeStale,
			Ref:    ref,
			Detail: "it changed after the read that issued this token",
			Fix:    fmt.Sprintf(`get(%q)`, ref),
		}
	}
	return nil
}

// AuthorizeAction accepts running an action against a ref the caller has read.
// The action names which read satisfies it, so a token from a different one is
// refused with that call rather than with a generic complaint (ADR-0015).
func (s *Store) AuthorizeAction(tokenID, ref, currentVersion string, from Origin) error {
	return s.authorizeFrom(tokenID, ref, currentVersion, from,
		"running an action requires having read what it acts on",
		"this action is satisfied by %s, and the token came from %s")
}

// AuthorizeUpdateFrom accepts a write whose token has to have come from one
// particular read, which is what makes reading the thing part of the contract
// rather than reading anything at all.
func (s *Store) AuthorizeUpdateFrom(tokenID, ref, currentVersion string, from Origin) error {
	return s.authorizeFrom(tokenID, ref, currentVersion, from,
		fmt.Sprintf("this write requires having read what it changes, with %s", from),
		"this write is satisfied by %s, and the token came from %s")
}

// authorizeFrom is the origin check both callers share, with their own wording:
// two copies of one rule is how they come to disagree.
func (s *Store) authorizeFrom(tokenID, ref, currentVersion string, from Origin, missing, wrong string) error {
	token := s.Lookup(tokenID)
	if token == nil {
		return &Rejection{
			Code:   CodeRequired,
			Ref:    ref,
			Detail: missing,
			Fix:    fmt.Sprintf(`%s(%q)`, from, ref),
		}
	}

	if from != "" && token.Origin != from {
		return &Rejection{
			Code:   CodeWrongRead,
			Ref:    ref,
			Detail: fmt.Sprintf(wrong, from, token.Origin),
			Fix:    fmt.Sprintf(`%s(%q)`, from, ref),
		}
	}
	return s.AuthorizeUpdate(tokenID, ref, currentVersion)
}

// AuthorizeCreate accepts a write creating a ref that does not exist. It needs
// a token from a search that did not return it, so an agent cannot duplicate
// something it never looked for.
func (s *Store) AuthorizeCreate(tokenID, ref string) error {
	token := s.Lookup(tokenID)
	if token == nil {
		return &Rejection{
			Code:   CodeRequired,
			Ref:    ref,
			Detail: "creating an entity requires having searched for it first",
			Fix:    fmt.Sprintf(`search(%q)`, ref),
		}
	}

	if token.Origin != FromSearch {
		return &Rejection{
			Code:   CodeSearchRequired,
			Ref:    ref,
			Detail: fmt.Sprintf("the token came from %s, which cannot witness that something is absent", token.Origin),
			Fix:    fmt.Sprintf(`search(%q)`, ref),
		}
	}
	if token.Covers(ref) {
		return &Rejection{
			Code:   CodeExists,
			Ref:    ref,
			Detail: "the search that issued this token already found it",
			Fix:    fmt.Sprintf(`declare(%q) as an update instead`, ref),
		}
	}
	return nil
}

func required(ref string) error {
	return &Rejection{
		Code:   CodeRequired,
		Ref:    ref,
		Detail: "no valid proof token was presented",
		Fix:    fmt.Sprintf(`get(%q)`, ref),
	}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return DefaultTTL
}

func (s *Store) staleLocked(t *Token) bool {
	return s.now().Sub(t.IssuedAt) > s.ttl()
}

// expireLocked drops tokens past the backstop, so a long-lived process does not
// accumulate every token it ever issued.
func (s *Store) expireLocked() {
	for id, token := range s.tokens {
		if s.staleLocked(token) {
			delete(s.tokens, id)
		}
	}
}

// newID uses crypto/rand.Text, which cannot fail, so minting a token needs no
// error path and this package keeps ADR-0017's no-panic rule.
func newID() string { return rand.Text() }
