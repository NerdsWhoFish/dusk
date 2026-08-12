// Package server serves onboarding, health, and (later) the API and UI.
package server

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/FetchHQ/dusk/internal/access"
	"github.com/FetchHQ/dusk/internal/config"
	"github.com/FetchHQ/dusk/internal/controller"
	"github.com/FetchHQ/dusk/internal/store"
	"github.com/FetchHQ/dusk/pkg/githubapp"
)

// credentialStore is the slice of store.Store the server needs, declared here
// so tests can substitute one without a data directory.
type credentialStore interface {
	Save(*store.Credentials) error
	Load() (*store.Credentials, error)
	Configured() bool
}

// catalogController is the slice of the controller the webhook path needs,
// declared here so the server does not depend on how reconciling works.
type catalogController interface {
	SyncPush(ctx context.Context, push controller.Push) error
	SyncPreview(ctx context.Context, preview controller.Preview) error
	Sync(ctx context.Context) error
}

// appClient exchanges manifest codes. Narrow enough to fake in tests.
type appClient interface {
	Convert(ctx context.Context, code string) (*githubapp.Credentials, error)
}

// Server routes HTTP for Dusk.
type Server struct {
	cfg         *config.Config
	credentials credentialStore
	github      appClient
	controller  catalogController
	catalog     Catalog
	syncs       Syncs
	pages       Pages
	access      *access.Policy
	oauth       *access.OAuth
	mcp         http.Handler
	state       *setupState
	deliveries  *seenDeliveries
	log         *slog.Logger
	now         func() time.Time
	tmpl        *template.Template
}

// Syncs reports what the controller last read, so the UI can tell a stale
// answer from a missing one.
type Syncs interface {
	Status() []controller.Status
}

// Options are the server's dependencies. Zero values get sane defaults.
type Options struct {
	Config      *config.Config
	Credentials credentialStore
	GitHub      appClient
	Controller  catalogController

	// Catalog and Syncs back the HTTP API and the UI. Optional, so a
	// deployment mid-onboarding still serves setup and health.
	Catalog Catalog
	Syncs   Syncs

	// Pages supplies the config repository's declared portal page. Optional:
	// without it the default page is served, which is the point of having one.
	Pages Pages

	// MCP serves the agent-facing surface. Optional, so a deployment can run
	// without it and so tests need not stand one up.
	MCP    http.Handler
	Logger *slog.Logger
	Now    func() time.Time
}

// New builds a Server.
func New(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, errors.New("server: config is required")
	}
	if opts.Credentials == nil {
		return nil, errors.New("server: credential store is required")
	}

	s := &Server{
		cfg:         opts.Config,
		credentials: opts.Credentials,
		github:      opts.GitHub,
		controller:  opts.Controller,
		catalog:     opts.Catalog,
		syncs:       opts.Syncs,
		pages:       opts.Pages,
		mcp:         opts.MCP,
		state:       newSetupState(),
		deliveries:  newSeenDeliveries(),
		log:         opts.Logger,
		now:         opts.Now,
	}
	if s.github == nil {
		s.github = &githubapp.Client{}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}

	// The cookie is marked Secure only behind TLS, or a local http run could
	// never log in: the browser would refuse to send back what it was given.
	s.access = access.New(s.cfg.MCPToken, s.cfg.TrustedNetwork,
		strings.HasPrefix(s.cfg.PrivateHost, "https://"))

	s.oauth = &access.OAuth{
		Credentials: s.signInCredentials,
		Callback:    s.cfg.SignInURL(),
		Policy:      s.access,
		GitHub:      &access.Client{Credentials: s.signInCredentials},
	}

	// Without this a completed sign-in sets an identity no gate consults, and
	// the browser is redirected straight back to the login page.
	s.access.Recognize(s.oauth)

	tmpl, err := template.New("").Parse(pages)
	if err != nil {
		return nil, err
	}
	s.tmpl = tmpl
	return s, nil
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	// Ready means "can serve HTTP", NOT "is onboarded". Gating readiness on
	// onboarding deadlocks: an unready pod gets no Service endpoints, so /setup
	// is unreachable, so it can never become onboarded.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if s.credentials.Configured() {
			_, _ = w.Write([]byte("ready\n"))
			return
		}
		_, _ = w.Write([]byte("ready, not onboarded: visit /setup\n"))
	})

	mux.HandleFunc("POST /webhooks", s.handleWebhook)

	if s.mcp != nil {
		mux.Handle("/mcp", s.mcp)
		mux.Handle("/mcp/", s.mcp)
	}

	mux.HandleFunc("GET /setup", s.handleSetup)
	mux.HandleFunc("GET /setup/callback", s.handleSetupCallback)
	mux.HandleFunc("GET /setup/done", s.handleSetupDone)

	// Signing in cannot sit behind the gate it opens.
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)

	// Signing in with GitHub is what makes authorization derived rather than
	// configured (ADR-0012). Both routes sit outside the gate they open.
	mux.HandleFunc("GET /auth/github", s.handleSignIn)
	mux.HandleFunc("GET /auth/callback", s.handleAuthCallback)

	if s.catalog != nil {
		mux.Handle("GET /api/", s.access.API(http.StripPrefix("/api", s.apiRoutes())))

		// Registered without a method: "GET /" would be narrower in method and
		// broader in path than /mcp/, which the mux rejects as ambiguous.
		mux.Handle("/", s.access.Browsers(http.HandlerFunc(s.handleApp)))
		return mux
	}

	// Anything else is a dead end until onboarding is done, so send people
	// where they can actually make progress.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !s.credentials.Configured() {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		http.NotFound(w, r)
	})

	return mux
}

// apiRoutes is the catalog over HTTP. The UI is an ordinary client of it with
// no privileged path, so everything the UI renders is fetchable with curl.
func (s *Server) apiRoutes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /search", s.handleAPISearch)
	api.HandleFunc("GET /entities", s.handleAPIEntities)
	api.HandleFunc("GET /entities/{ref}", s.handleAPIEntity)
	api.HandleFunc("GET /entities/{ref}/dependents", s.handleAPIDependents)
	api.HandleFunc("GET /status", s.handleAPIStatus)
	api.HandleFunc("GET /overview", s.handleAPIOverview)
	api.HandleFunc("GET /integrity", s.handleAPIIntegrity)
	api.HandleFunc("GET /drift", s.handleAPIDrift)
	api.HandleFunc("GET /home", s.handleAPIHome)
	api.HandleFunc("GET /viewer", s.handleAPIViewer)
	api.HandleFunc("GET /diff", s.handleAPIDiff)
	return api
}

type setupPage struct {
	Base        string
	WebhookURL  string
	SplitHosts  bool
	Mode        string
	Org         string
	Action      string
	State       string
	Manifest    string
	Permissions map[string]string
}

type donePage struct {
	Name        string
	Slug        string
	Mode        string
	InstallURL  string
	Permissions map[string]string
}

type failPage struct {
	Title  string
	Detail string
	Retry  string
	Status int
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render failed", "template", name, "error", err)
	}
}

// fail renders an error page that says what happened and what to do about it,
// per the anomalies-surfaced rule in docs/philosophy.md.
func (s *Server) fail(w http.ResponseWriter, _ *http.Request, status int, title, detail, retry string) {
	s.log.Error("setup failed", "title", title, "detail", detail, "status", status)
	w.WriteHeader(status)
	s.render(w, "fail", failPage{Title: title, Detail: strings.TrimSpace(detail), Retry: retry, Status: status})
}
