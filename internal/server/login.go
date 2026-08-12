package server

import (
	"net/http"
)

// handleLoginPage shows the sign-in form, or sends an already-signed-in browser
// on. A deployment on a trusted network needs no login at all, so asking for
// one there would invent a step that does nothing.
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !s.access.Required() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
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
		s.log.Warn("rejected a sign-in", "remote", r.RemoteAddr)
		s.renderLogin(w, r, http.StatusUnauthorized, "That is not the token.")
		return
	}

	http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.access.LogOut(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) renderLogin(w http.ResponseWriter, _ *http.Request, status int, problem string) {
	// Content-Type before WriteHeader, or the status is sent with the headers
	// already frozen and the browser gets text/plain.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	page := loginPage{Problem: problem, GitHub: s.oauth.Configured()}
	if err := s.tmpl.ExecuteTemplate(w, "login", page); err != nil {
		s.log.Error("could not render the login page", "error", err)
	}
}

type loginPage struct {
	Problem string
	GitHub  bool
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
