package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/FetchHQ/dusk/internal/store"
	"github.com/FetchHQ/dusk/pkg/githubapp"
)

// stateTTL bounds how long a started setup may sit before its callback is
// refused. GitHub's own manifest code expires in an hour.
const stateTTL = time.Hour

type pendingSetup struct {
	mode    store.AccessMode
	created time.Time
}

// setupState holds in-flight setup attempts. It is deliberately in memory:
// a restart mid-setup means starting over, which is cheap and correct.
type setupState struct {
	mu      sync.Mutex
	pending map[string]pendingSetup
}

func newSetupState() *setupState {
	return &setupState{pending: make(map[string]pendingSetup)}
}

func (s *setupState) begin(mode store.AccessMode, now time.Time) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("server: generate state: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.pending {
		if now.Sub(v.created) > stateTTL {
			delete(s.pending, k)
		}
	}
	s.pending[token] = pendingSetup{mode: mode, created: now}
	return token, nil
}

// consume validates and removes a token, so a callback cannot be replayed.
func (s *setupState) consume(token string, now time.Time) (store.AccessMode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pending[token]
	if !ok {
		return "", false
	}
	delete(s.pending, token)
	if now.Sub(p.created) > stateTTL {
		return "", false
	}
	return p.mode, true
}

// permissionsFor returns only what the chosen mode needs, so the install
// screen shows the truth and read-only really is read-only.
//
// pull_requests is read in every mode because PR previews are a core feature,
// and GitHub refuses a manifest whose events exceed its permissions.
func permissionsFor(mode store.AccessMode) map[string]string {
	perms := map[string]string{
		"metadata":      "read",
		"contents":      "read",
		"pull_requests": "read",
	}
	switch mode {
	case store.ModeProposal:
		perms["pull_requests"] = "write"
	case store.ModeWrite:
		perms["contents"] = "write"
	case store.ModeRead:
	}
	return perms
}

// defaultEvents omits installation and installation_repositories: GitHub
// delivers those to every App and rejects a manifest that declares them.
func defaultEvents() []string {
	return []string{"push", "pull_request"}
}

func (s *Server) manifest(mode store.AccessMode) githubapp.Manifest {
	return githubapp.Manifest{
		Name:        "Dusk",
		URL:         s.cfg.PrivateHost,
		Description: "A service catalog that maintains itself.",
		HookAttributes: githubapp.HookAttributes{
			URL:    s.cfg.WebhookURL(),
			Active: true,
		},
		RedirectURL:        s.cfg.SetupCallbackURL(),
		CallbackURLs:       []string{s.cfg.AuthCallbackURL()},
		Public:             false,
		DefaultEvents:      defaultEvents(),
		DefaultPermissions: permissionsFor(mode),
	}
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.credentials.Configured() {
		http.Redirect(w, r, "/setup/done", http.StatusSeeOther)
		return
	}

	mode := store.AccessMode(r.URL.Query().Get("mode"))
	if !mode.Valid() {
		mode = store.ModeProposal
	}

	token, err := s.state.begin(mode, s.now())
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Could not start setup", err.Error(), "")
		return
	}
	manifestJSON, err := githubapp.ManifestJSON(s.manifest(mode))
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Could not build the App manifest", err.Error(), "")
		return
	}

	org := r.URL.Query().Get("org")
	action := githubapp.CreateURL
	if org != "" {
		action = githubapp.OrgCreateURL(org)
	}

	s.render(w, "setup", setupPage{
		Base:        s.cfg.PrivateHost,
		WebhookURL:  s.cfg.WebhookURL(),
		SplitHosts:  s.cfg.SplitHosts(),
		Mode:        string(mode),
		Org:         org,
		Action:      action,
		State:       token,
		Manifest:    manifestJSON,
		Permissions: permissionsFor(mode),
	})
}

func (s *Server) handleSetupCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		s.fail(w, r, http.StatusBadRequest, "GitHub sent no code",
			"The callback arrived without a code parameter.", "/setup")
		return
	}

	mode, ok := s.state.consume(r.URL.Query().Get("state"), s.now())
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "This setup link is stale",
			"Setup must finish within an hour of starting, and each attempt can only be used once. Start again.", "/setup")
		return
	}

	creds, err := s.github.Convert(r.Context(), code)
	if err != nil {
		s.fail(w, r, http.StatusBadGateway, "GitHub would not exchange the code", err.Error(), "/setup")
		return
	}
	if err := s.credentials.Save(store.FromGitHub(creds, mode)); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Could not save the credentials", err.Error(), "")
		return
	}

	s.log.Info("onboarding complete", "app_id", creds.ID, "slug", creds.Slug, "mode", string(mode))
	http.Redirect(w, r, "/setup/done", http.StatusSeeOther)
}

func (s *Server) handleSetupDone(w http.ResponseWriter, r *http.Request) {
	creds, err := s.credentials.Load()
	if err != nil {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	s.render(w, "done", donePage{
		Name:        creds.Name,
		Slug:        creds.Slug,
		Mode:        string(creds.Mode),
		InstallURL:  creds.InstallURL(),
		Permissions: permissionsFor(creds.Mode),
	})
}
