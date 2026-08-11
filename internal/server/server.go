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

	"github.com/FetchHQ/dusk/internal/config"
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

// appClient exchanges manifest codes. Narrow enough to fake in tests.
type appClient interface {
	Convert(ctx context.Context, code string) (*githubapp.Credentials, error)
}

// Server routes HTTP for Dusk.
type Server struct {
	cfg         *config.Config
	credentials credentialStore
	github      appClient
	state       *setupState
	deliveries  *seenDeliveries
	log         *slog.Logger
	now         func() time.Time
	tmpl        *template.Template
}

// Options are the server's dependencies. Zero values get sane defaults.
type Options struct {
	Config      *config.Config
	Credentials credentialStore
	GitHub      appClient
	Logger      *slog.Logger
	Now         func() time.Time
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

	mux.HandleFunc("GET /setup", s.handleSetup)
	mux.HandleFunc("GET /setup/callback", s.handleSetupCallback)
	mux.HandleFunc("GET /setup/done", s.handleSetupDone)

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
