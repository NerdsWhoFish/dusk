package mcp

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/FetchHQ/dusk/pkg/secret"
)

// RequireBearer refuses any request not presenting token. This surface returns
// the catalog wholesale, so leaving it open hands every entity to anything that
// can reach the port.
func RequireBearer(next http.Handler, token secret.String) http.Handler {
	want := []byte(token.Reveal())

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		// Compared in constant time so a wrong token cannot be found one
		// character at a time.
		if !ok || subtle.ConstantTimeCompare([]byte(presented), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="dusk"`)
			http.Error(w, "the catalog requires a bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Disabled answers where the surface would be, saying why it is off and what
// turns it on. A bare 404 would read as a bug in Dusk rather than a decision.
func Disabled() http.Handler {
	const explanation = `The agent surface is off because nothing has said who may read the catalog.

Set DUSK_MCP_TOKEN to a secret and present it as a bearer token, or set
DUSK_TRUSTED_NETWORK=true to serve it unauthenticated on a network you trust.

Dusk will not choose for you: reading this surface returns the whole catalog.
`

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, explanation, http.StatusServiceUnavailable)
	})
}
