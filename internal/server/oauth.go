package server

import (
	"net/http"

	"github.com/NerdsWhoFish/dusk/internal/access"
	"github.com/NerdsWhoFish/dusk/internal/index"
)

// handleSignIn sends the browser to GitHub.
func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.oauth.Configured() {
		http.Error(w, "signing in with GitHub is not configured on this deployment", http.StatusNotFound)
		return
	}

	target, err := s.oauth.Begin()
	if err != nil {
		s.log.Error("could not start a sign-in", "error", err)
		http.Error(w, "could not start the sign-in", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleAuthCallback finishes the sign-in and records who the person is.
func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !s.oauth.Configured() {
		http.NotFound(w, r)
		return
	}

	id, identity, err := s.oauth.Complete(r.Context(),
		r.URL.Query().Get("code"), r.URL.Query().Get("state"))
	if err != nil {
		s.log.Warn("a sign-in failed", "error", err, "remote", r.RemoteAddr)
		s.renderLogin(w, r, http.StatusUnauthorized, "That sign-in did not complete. Try again.")
		return
	}

	s.oauth.SetIdentity(w, id)
	s.log.Info("signed in with github",
		"login", identity.Login, "readable_repositories", len(identity.Readable))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// visibilityFor derives what this request may see. No OAuth means everything,
// the single-operator posture of ADR-0027; once somebody signs in, what they
// may read is GitHub's answer rather than anything configured here.
func (s *Server) visibilityFor(r *http.Request) index.Visibility {
	if !s.oauth.Configured() {
		return index.Unrestricted()
	}

	identity, ok := s.oauth.Identify(r)
	if !ok {
		return index.Unrestricted()
	}
	return index.Visibility{
		Repositories: identity.Readable,
		Observed:     s.cfg.ShowObservedToEveryone,
	}
}

// signedInAs reports who the viewer is, for the UI to show.
func (s *Server) signedInAs(r *http.Request) (access.Identity, bool) {
	if !s.oauth.Configured() {
		return access.Identity{}, false
	}
	return s.oauth.Identify(r)
}
