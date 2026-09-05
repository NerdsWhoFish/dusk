package server

import (
	"net/http"

	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

// signInCredentials resolves who Dusk signs people in as. The registered App is
// itself an OAuth provider and its manifest already claims the callback, so a
// working install needs nothing configured. The environment wins where set.
func (s *Server) signInCredentials() (string, secret.String) {
	if s.cfg.OAuthConfigured() {
		return s.cfg.OAuthClientID, s.cfg.OAuthClientSecret
	}

	app, err := s.credentials.Load()
	if err != nil || app == nil {
		return "", secret.String{}
	}
	return app.ClientID, app.ClientSecret
}

// handleLoginPage shows the sign-in form, or sends an already-signed-in browser
// on. A deployment on a trusted network needs no login at all, so asking for
// one there would invent a step that does nothing.
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !s.access.Required() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if s.oauth.Configured() && r.URL.Query().Get("method") == "" && r.URL.Query().Get("logged_out") == "" {
		http.Redirect(w, r, "/auth/github", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, r, http.StatusOK, "")
}

// handleLogin exchanges the token for a session cookie. A browser cannot send
// an Authorization header on a navigation, so this is the only way a person
// reaches a UI sitting behind the same gate as the agent surface.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.access.Required() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, r, http.StatusBadRequest, "That form could not be read.")
		return
	}

	if !s.access.LogIn(w, r.PostFormValue("token")) {
		// Deliberately vague and deliberately not logged with the value: the
		// only person who should learn anything here is one who has the token.
		s.log.WarnContext(r.Context(), "rejected a sign-in")
		s.renderLogin(w, r, http.StatusUnauthorized, "That is not the token.")
		return
	}

	http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.access.LogOut(w)
	s.oauth.ClearIdentity(w, r)
	http.Redirect(w, r, "/login?logged_out=1", http.StatusSeeOther)
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, status int, problem string) {
	// Content-Type before WriteHeader, or the status is sent with the headers
	// already frozen and the browser gets text/plain.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	github := s.oauth.Configured()
	page := loginPage{
		Problem:   problem,
		GitHub:    github,
		Token:     !github || r.URL.Query().Get("method") == "token" || problem != "",
		LoggedOut: r.URL.Query().Get("logged_out") != "",
	}
	if err := s.tmpl.ExecuteTemplate(w, "login", page); err != nil {
		s.log.Error("could not render the login page", "error", err)
	}
}

type loginPage struct {
	Problem   string
	GitHub    bool
	Token     bool
	LoggedOut bool
}

// safeNext keeps a redirect inside this site. A returned path from a query
// string is attacker-supplied, and without this the login page becomes an open
// redirect that lends Dusk's hostname to somebody else's page.
func safeNext(next string) string {
	if len(next) < 2 || next[0] != '/' || next[1] == '/' {
		return "/"
	}
	return next
}
