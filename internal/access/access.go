// Package access answers who may read the catalog.
//
// One question, three credentials: a bearer token, a cookie proving the holder
// knew that token, and a GitHub identity. All answer to one policy, so a
// deployment cannot end up with a locked agent surface and an open UI.
package access

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

// SessionCookie is the browser's credential.
const SessionCookie = "dusk_session"

// SessionLife is how long a browser stays signed in. Long, because this is one
// operator on their own network and a daily login is friction with no attacker
// it inconveniences.
const SessionLife = 30 * 24 * time.Hour

// Mode is what a deployment decided about readers.
type Mode string

const (
	// ModeToken requires the token, as a header or a session.
	ModeToken Mode = "token"

	// ModeTrusted serves anyone who can reach the port.
	ModeTrusted Mode = "unauthenticated"

	// ModeOff serves nobody, because nothing said who may read.
	ModeOff Mode = "off"
)

// Identities recognizes somebody who signed in with GitHub.
type Identities interface {
	Identify(*http.Request) (Identity, bool)
}

// Policy decides who is let in.
type Policy struct {
	token   secret.String
	trusted bool

	// identities is nil until sign-in with GitHub is wired up, which leaves the
	// shared token as the only credential.
	identities Identities

	// secure marks the cookie Secure, which a browser then refuses to send
	// over plain http. Off for a local run, on for a real deployment.
	secure bool

	now func() time.Time
}

// New builds the policy. A token wins over a trusted network, because the
// stricter answer is the safer one to default to when both are set.
func New(token secret.String, trustedNetwork, secure bool) *Policy {
	return &Policy{token: token, trusted: trustedNetwork, secure: secure, now: time.Now}
}

// Mode reports the decision, for the boot log.
func (p *Policy) Mode() Mode {
	switch {
	case !p.token.IsZero():
		return ModeToken
	case p.trusted:
		return ModeTrusted
	default:
		return ModeOff
	}
}

// Agents guards a surface read by programs, which present a bearer token.
func (p *Policy) Agents(next http.Handler) http.Handler {
	return p.guard(next, func(r *http.Request) bool {
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		return ok && p.correct(presented)
	}, func(w http.ResponseWriter) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="dusk"`)
		http.Error(w, "the catalog requires a bearer token", http.StatusUnauthorized)
	})
}

// Browsers guards a surface read by people, who present a session cookie. An
// unauthenticated browser is redirected rather than shown a 401, because a
// person who cannot see the catalog wants the login page, not the error.
func (p *Policy) Browsers(next http.Handler) http.Handler {
	return p.guard(next, p.signedIn, func(w http.ResponseWriter) {
		w.Header().Set("Location", "/login")
		w.WriteHeader(http.StatusSeeOther)
	})
}

// API guards the HTTP API, which both a browser and a script call, so it takes
// either credential and answers a refusal the way a fetch can read.
func (p *Policy) API(next http.Handler) http.Handler {
	return p.guard(next, func(r *http.Request) bool {
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		return (ok && p.correct(presented)) || p.signedIn(r)
	}, func(w http.ResponseWriter) {
		http.Error(w, `{"error":"not authorized"}`, http.StatusUnauthorized)
	})
}

func (p *Policy) guard(next http.Handler, allowed func(*http.Request) bool, refuse func(http.ResponseWriter)) http.Handler {
	switch p.Mode() {
	case ModeTrusted:
		return next
	case ModeOff:
		return Disabled()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed(r) {
			refuse(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LogIn exchanges the token for a session cookie. A browser cannot send an
// Authorization header on a navigation, so this is how a person reaches a UI
// that is otherwise behind the same gate as the agent surface.
func (p *Policy) LogIn(w http.ResponseWriter, presented string) bool {
	if !p.correct(presented) {
		return false
	}

	expires := p.now().Add(SessionLife)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    p.mint(expires),
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   p.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return true
}

// LogOut clears the session.
func (p *Policy) LogOut(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   p.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Required reports whether a person has to log in at all.
func (p *Policy) Required() bool { return p.Mode() == ModeToken }

// Recognize teaches the policy about people signed in with GitHub. Without it
// a successful sign-in sets a cookie no gate ever reads, and the browser lands
// back on the login page holding a perfectly good identity.
func (p *Policy) Recognize(identities Identities) { p.identities = identities }

func (p *Policy) signedIn(r *http.Request) bool {
	if cookie, err := r.Cookie(SessionCookie); err == nil && p.valid(cookie.Value) {
		return true
	}
	if p.identities == nil {
		return false
	}
	_, ok := p.identities.Identify(r)
	return ok
}

// mint signs an expiry with a key derived from the token, so the cookie proves
// the holder knew it without carrying it, and rotating the token ends every
// session without any server-side store to expire.
func (p *Policy) mint(expires time.Time) string {
	stamp := strconv.FormatInt(expires.Unix(), 10)
	return stamp + "." + p.sign(stamp)
}

func (p *Policy) valid(value string) bool {
	stamp, signature, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(signature), []byte(p.sign(stamp))) != 1 {
		return false
	}

	// Checked after the signature so an unsigned value cannot probe for a
	// timing difference in the parse.
	expires, err := strconv.ParseInt(stamp, 10, 64)
	return err == nil && p.now().Before(time.Unix(expires, 0))
}

func (p *Policy) sign(stamp string) string {
	mac := hmac.New(sha256.New, []byte("dusk-session\x00"+p.token.Reveal()))
	mac.Write([]byte(stamp))
	return hex.EncodeToString(mac.Sum(nil))
}

func (p *Policy) correct(presented string) bool {
	want := []byte(p.token.Reveal())
	return subtle.ConstantTimeCompare([]byte(presented), want) == 1
}

// Disabled answers where a surface would be, saying why it is off and what
// turns it on. A bare 404 would read as a bug in Dusk rather than a decision.
func Disabled() http.Handler {
	const explanation = `The catalog is not being served because nothing has said who may read it.

Set DUSK_MCP_TOKEN to a secret and present it as a bearer token, or sign in
with it in a browser, or set DUSK_TRUSTED_NETWORK=true to serve it
unauthenticated on a network you trust.

Dusk will not choose for you: reading this returns the whole catalog.
`

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, explanation, http.StatusServiceUnavailable)
	})
}
