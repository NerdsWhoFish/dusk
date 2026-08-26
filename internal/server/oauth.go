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

// visibilityFor returns the single operator view. GitHub establishes that the
// operator belongs to this installation, then grants the same view as the
// bearer token (ADR-0084).
func (s *Server) visibilityFor(*http.Request) index.Visibility { return index.Unrestricted() }

// signedInAs reports who the viewer is, for the UI to show.
func (s *Server) signedInAs(r *http.Request) (access.Identity, bool) {
	if !s.oauth.Configured() {
		return access.Identity{}, false
	}
	return s.oauth.Identify(r)
}
