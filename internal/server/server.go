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

	"github.com/NerdsWhoFish/dusk/internal/access"
	"github.com/NerdsWhoFish/dusk/internal/answer"
	"github.com/NerdsWhoFish/dusk/internal/config"
	"github.com/NerdsWhoFish/dusk/internal/controller"
	"github.com/NerdsWhoFish/dusk/internal/events"
	"github.com/NerdsWhoFish/dusk/internal/insights"
	"github.com/NerdsWhoFish/dusk/internal/store"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
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
	SharesRepository(ctx context.Context, readable []string) (bool, error)
}

// appClient exchanges manifest codes. Narrow enough to fake in tests.
type appClient interface {
	Convert(ctx context.Context, code string) (*githubapp.Credentials, error)
}

// Server routes HTTP for Dusk.
type Server struct {
	cfg          *config.Config
	credentials  credentialStore
	github       appClient
	controller   catalogController
	catalog      Catalog
	plugins      Plugins
	rotation     Rotation
	syncs        Syncs
	pages        Pages
	notes        Notes
	agentContext AgentContext
	contextFile  ContextFile
	repositories RepositoryFiles
	declarations Declarations
	answers      *answer.Service
	events       *events.Log
	insights     Insights
	tokens       *proof.Store
	access       *access.Policy
	oauth        *access.OAuth
	mcp          http.Handler
	state        *setupState
	deliveries   *seenDeliveries
	log          *slog.Logger
	now          func() time.Time
	tmpl         *template.Template
}

// Syncs reports what the controller last read, so the UI can tell a stale
// answer from a missing one.
type Syncs interface {
	Status() []controller.Status
}

// Insights derives the local analytics block shown on the operator dashboard.
type Insights interface {
	Read(context.Context) (insights.Snapshot, error)
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

	// Plugins backs the marketplace. Optional: without it the plugin pages
	// answer empty rather than failing, so a deployment that installs nothing
	// is not a deployment with broken routes.
	Plugins Plugins

	// Rotation is what is currently observing. Without it Dusk cannot tell an
	// abandoned observation scope from a live one, so it reports neither.
	Rotation Rotation

	// Notes writes notes. Optional: without it a note cannot be closed from the
	// browser, which is what a deployment with no config repository looks like.
	Notes Notes

	// AgentContext renders exactly what dusk_context returns. ContextFile reads
	// and writes the Git-backed policy that controls that rendering.
	AgentContext AgentContext
	ContextFile  ContextFile

	// Repositories reads and writes each repository's root dusk.md through the
	// same proof-gated Git path offered to agents.
	Repositories RepositoryFiles

	// Declarations writes entity changes through the same proof-gated Git path
	// used by agents.
	Declarations Declarations

	// Events is what has been run. Optional: without it the events route
	// answers empty rather than failing.
	Events *events.Log

	// Insights combines current catalog shape with the retained action log.
	// Optional: without it an analytics block explains why it is empty.
	Insights Insights

	// Answers backs the optional grounded AI mode in search. Without it the
	// configuration route says disabled and the ordinary search is unchanged.
	Answers *answer.Service

	// Tokens issues the proof an action presents. The browser gets one on every
	// entity read, which is the same read-before-write contract an agent meets
	// and gives the UI optimistic concurrency for free (ADR-0009).
	Tokens *proof.Store

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
		cfg:          opts.Config,
		credentials:  opts.Credentials,
		github:       opts.GitHub,
		controller:   opts.Controller,
		catalog:      opts.Catalog,
		syncs:        opts.Syncs,
		pages:        opts.Pages,
		plugins:      opts.Plugins,
		rotation:     opts.Rotation,
		notes:        opts.Notes,
		agentContext: opts.AgentContext,
		contextFile:  opts.ContextFile,
		repositories: opts.Repositories,
		declarations: opts.Declarations,
		answers:      opts.Answers,
		events:       opts.Events,
		insights:     opts.Insights,
		tokens:       opts.Tokens,
		mcp:          opts.MCP,
		state:        newSetupState(),
		deliveries:   newSeenDeliveries(),
		log:          opts.Logger,
		now:          opts.Now,
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
		Admit:       s.admitGitHub,
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
	mux.HandleFunc("GET /setup/installed", s.handleSetupInstalled)

	// Nor can the icons those pages reference, and they exist before onboarding
	// does, so they are routed whether or not there is a catalog.
	for _, icon := range icons {
		mux.HandleFunc("GET "+icon, s.handleIcon)
	}

	// A plugin's view is served from Dusk's own origin, so the browser never
	// fetches a plugin's code from anywhere else (ADR-0020).
	mux.HandleFunc("GET /plugin-assets/{plugin}/{name}", s.handlePluginAsset)

	// Signing in cannot sit behind the gate it opens.
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)

	// Signing in with GitHub is what makes authorization derived rather than
	// configured (ADR-0012). Both routes sit outside the gate they open.
	mux.HandleFunc("GET /auth/github", s.handleSignIn)
	mux.HandleFunc("GET /auth/callback", s.handleAuthCallback)
	mux.HandleFunc("GET /telemetry/config", s.handleTelemetryConfig)

	if s.catalog != nil {
		// No method: the API answers POST as well as GET, and "GET /api/" let
		// every write fall through to the browser catch-all, which redirected
		// it to the login page as though the session had lapsed.
		mux.Handle("/api/", s.access.API(http.StripPrefix("/api", s.apiRoutes())))

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
	api.HandleFunc("GET /ai", s.handleAPIAnswerConfig)
	api.HandleFunc("POST /ai/ask", s.handleAPIAsk)
	api.HandleFunc("GET /graph", s.handleAPIGraph)
	api.HandleFunc("GET /entities", s.handleAPIEntities)
	api.HandleFunc("GET /entities/{ref}", s.handleAPIEntity)
	api.HandleFunc("POST /entities/{ref}", s.handleAPISetEntity)
	api.HandleFunc("GET /notes", s.handleAPINotes)
	api.HandleFunc("POST /notes", s.handleAPIWriteNote)
	api.HandleFunc("GET /notes/{id}", s.handleAPINote)
	api.HandleFunc("POST /notes/delete", s.handleAPIDeleteNote)
	api.HandleFunc("GET /entities/{ref}/dependents", s.handleAPIDependents)
	api.HandleFunc("GET /status", s.handleAPIStatus)
	api.HandleFunc("GET /overview", s.handleAPIOverview)
	api.HandleFunc("GET /kinds", s.handleAPIKinds)
	api.HandleFunc("GET /integrity", s.handleAPIIntegrity)
	api.HandleFunc("GET /drift", s.handleAPIDrift)
	api.HandleFunc("GET /home", s.handleAPIHome)
	api.HandleFunc("GET /context", s.handleAPIContext)
	api.HandleFunc("POST /context", s.handleAPISetContext)
	api.HandleFunc("GET /repository", s.handleAPIRepository)
	api.HandleFunc("POST /repository", s.handleAPISetRepository)
	api.HandleFunc("GET /viewer", s.handleAPIViewer)
	api.HandleFunc("GET /diff", s.handleAPIDiff)

	// Installing runs somebody else's binary, so these sit behind the same
	// gate as everything else and are POST rather than GET.
	api.HandleFunc("POST /observations/forget", s.handleAPIForget)
	api.HandleFunc("GET /plugins", s.handleAPIPlugins)
	api.HandleFunc("POST /plugins/refresh", s.handleAPIRefresh)
	api.HandleFunc("POST /plugins/{id}/install", s.handleAPIInstall)
	api.HandleFunc("POST /plugins/{id}/uninstall", s.handleAPIUninstall)
	api.HandleFunc("POST /plugins/{id}/restart", s.handleAPIRestart)
	api.HandleFunc("POST /plugins/{id}/config", s.handleAPIConfigure)
	api.HandleFunc("POST /plugins/{id}/config/{instance}", s.handleAPIConfigure)
	api.HandleFunc("GET /plugins/{id}/output", s.handleAPIOutput)

	// One declaration, three surfaces: these run the same actions an agent
	// invokes and the same ones the entity page offers (ADR-0041).
	api.HandleFunc("POST /entities/{ref}/actions/{action}", s.handleAPIInvoke)
	api.HandleFunc("POST /plugins/{id}/actions/{action}", s.handleAPIPluginInvoke)
	api.HandleFunc("POST /plugins/{id}/actions/{action}/enabled", s.handleAPIEnableAction)
	api.HandleFunc("GET /plugins/{id}/handles/{handle}", s.handleAPIActionStatus)
	api.HandleFunc("GET /events", s.handleAPIEvents)
	api.HandleFunc("POST /notes/status", s.handleAPINoteStatus)
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
