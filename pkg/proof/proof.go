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

	// FromNote is reading what has been written down. A note's id is a file
	// path, so nothing else can name one: `get` takes entity refs.
	FromNote Origin = "note"

	// FromPage is reading the portal page, which is the only read that
	// witnesses the whole of what a write would replace.
	FromPage Origin = "page"

	// FromContext is reading the operator's agent context profile before
	// replacing it through the browser.
	FromContext Origin = "context"

	// FromKinds is reading the vocabulary. Extending one you have not read is
	// how `svc` gets invented next to `service` (ADR-0048).
	FromKinds Origin = "kinds"

	// FromConfigure is reading a plugin configuration before replacing it.
	FromConfigure Origin = "configure"
)

// Call renders the invocation that re-reads ref, which is what a rejection
// names. There is no one shape: `get` takes an entity ref, `note` takes the
// path that is a note's id, and `kinds` and `page` take nothing at all.
func (o Origin) Call(ref string) string {
	switch o {
	case FromNote:
		return fmt.Sprintf("note(id: %q)", ref)
	case FromKinds, FromPage, FromContext:
		return string(o) + "()"
	case FromConfigure:
		return "configure()"
	case FromSearch, FromNeighbors:
		return fmt.Sprintf("%s(%q)", o, ref)
	default:
		return fmt.Sprintf("%s(%q)", FromGet, ref)
	}
}

// Subject is what a write is against: the ref a token has to cover, and the
// read that re-reads it. Two facts rather than one, because a note is covered
// by its file path and re-read by `note`, and nothing turns one into the other.
type Subject struct {
	Ref string

	// Read is the tool that re-reads Ref. The zero value is get, which is the
	// recovery for an entity.
	Read Origin
}

// Entity is an entity ref, re-read by get.
func Entity(ref string) Subject { return Subject{Ref: ref, Read: FromGet} }

// Note is a note by its id, which is its path, re-read by note.
func Note(id string) Subject { return Subject{Ref: id, Read: FromNote} }

// Portal is the homepage at path, re-read by page.
func Portal(path string) Subject { return Subject{Ref: path, Read: FromPage} }

// ContextProfile is the operator's agent orientation policy, re-read by the
// context UI before it is replaced.
func ContextProfile(path string) Subject { return Subject{Ref: path, Read: FromContext} }

// Vocabulary is the minted kinds at path, re-read by kinds.
func Vocabulary(path string) Subject { return Subject{Ref: path, Read: FromKinds} }

// Configuration is one plugin instance's settings, re-read by configure.
func Configuration(plugin, instance string) Subject {
	return Subject{Ref: "plugin-config:" + plugin + ":" + instance, Read: FromConfigure}
}

// fix is the call that recovers from a rejection against this subject.
func (s Subject) fix() string { return s.Read.Call(s.Ref) }

// witness is the read that can see this subject is absent, which is what a
// create is authorized by. Nothing but a search enumerates entities, and a
// thing at a fixed path is witnessed by its own read: search cannot name one.
func (s Subject) witness() Origin {
	if s.Read == "" || s.Read == FromGet {
		return FromSearch
	}
	return s.Read
}

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

// AuthorizeUpdate accepts a write to a subject that already exists. A token
// recording a version other than currentVersion means somebody wrote in
// between, which is the collision this gate exists to catch.
func (s *Store) AuthorizeUpdate(tokenID string, subject Subject, currentVersion string) error {
	token := s.Lookup(tokenID)
	if token == nil {
		return &Rejection{
			Code:   CodeRequired,
			Ref:    subject.Ref,
			Detail: "no valid proof token was presented",
			Fix:    subject.fix(),
		}
	}

	seenVersion, covered := token.seen[subject.Ref]
	if !covered {
		return &Rejection{
			Code:   CodeUnseen,
			Ref:    subject.Ref,
			Detail: "the token presented came from a read that did not return this",
			Fix:    subject.fix(),
		}
	}
	if seenVersion != currentVersion {
		return &Rejection{
			Code:   CodeStale,
			Ref:    subject.Ref,
			Detail: "it changed after the read that issued this token",
			Fix:    subject.fix(),
		}
	}
	return nil
}

// AuthorizeAction accepts running an action against a ref the caller has read.
// The action names which read satisfies it, so a token from a different one is
// refused with that call rather than with a generic complaint (ADR-0015).
func (s *Store) AuthorizeAction(tokenID, ref, currentVersion string, from Origin) error {
	return s.authorizeFrom(tokenID, Subject{Ref: ref, Read: from}, currentVersion,
		"running an action requires having read what it acts on",
		"this action is satisfied by %s, and the token came from %s")
}

// AuthorizeUpdateFrom accepts a write whose token has to have come from the
// subject's own read, which is what makes reading the thing part of the
// contract rather than reading anything at all.
func (s *Store) AuthorizeUpdateFrom(tokenID string, subject Subject, currentVersion string) error {
	return s.authorizeFrom(tokenID, subject, currentVersion,
		fmt.Sprintf("this write requires having read what it changes, with %s", subject.Read),
		"this write is satisfied by %s, and the token came from %s")
}

// authorizeFrom is the origin check both callers share, with their own wording:
// two copies of one rule is how they come to disagree.
func (s *Store) authorizeFrom(tokenID string, subject Subject, currentVersion string, missing, wrong string) error {
	token := s.Lookup(tokenID)
	if token == nil {
		return &Rejection{
			Code:   CodeRequired,
			Ref:    subject.Ref,
			Detail: missing,
			Fix:    subject.fix(),
		}
	}

	if subject.Read != "" && token.Origin != subject.Read {
		return &Rejection{
			Code:   CodeWrongRead,
			Ref:    subject.Ref,
			Detail: fmt.Sprintf(wrong, subject.Read, token.Origin),
			Fix:    subject.fix(),
		}
	}
	return s.AuthorizeUpdate(tokenID, subject, currentVersion)
}

// AuthorizeCreate accepts a write creating a subject that does not exist. It
// needs a token from a read that did not return it and could have, so an agent
// cannot duplicate something it never looked for.
func (s *Store) AuthorizeCreate(tokenID string, subject Subject) error {
	witness := subject.witness()

	token := s.Lookup(tokenID)
	if token == nil {
		return &Rejection{
			Code:   CodeRequired,
			Ref:    subject.Ref,
			Detail: fmt.Sprintf("creating this requires having looked for it first, with %s", witness),
			Fix:    witness.Call(subject.Ref),
		}
	}

	if token.Origin != witness {
		return &Rejection{
			Code:   CodeSearchRequired,
			Ref:    subject.Ref,
			Detail: fmt.Sprintf("the token came from %s, which cannot witness that this is absent", token.Origin),
			Fix:    witness.Call(subject.Ref),
		}
	}
	if token.Covers(subject.Ref) {
		// An entity is updated by declaring it again; anything else is read and
		// written by the one tool that already named it.
		fix := subject.fix()
		if witness == FromSearch {
			fix = fmt.Sprintf(`declare(%q) as an update instead`, subject.Ref)
		}
		return &Rejection{
			Code:   CodeExists,
			Ref:    subject.Ref,
			Detail: "the read that issued this token already found it",
			Fix:    fix,
		}
	}
	return nil
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
