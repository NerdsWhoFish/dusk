package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NerdsWhoFish/dusk/internal/answer"
	"github.com/NerdsWhoFish/dusk/internal/config"
	"github.com/NerdsWhoFish/dusk/internal/controller"
	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/server"
	"github.com/NerdsWhoFish/dusk/internal/store"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/vault"
)

const externalURL = "https://dusk.example.com"

type fakeStore struct {
	creds     *store.Credentials
	saveErr   error
	savedMode store.AccessMode
}

func (f *fakeStore) Save(c *store.Credentials) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.creds = c
	f.savedMode = c.Mode
	return nil
}

func (f *fakeStore) Load() (*store.Credentials, error) {
	if f.creds == nil {
		return nil, store.ErrNotConfigured
	}
	return f.creds, nil
}

func (f *fakeStore) Configured() bool { return f.creds != nil }

type fakeGitHub struct {
	creds *githubapp.Credentials
	err   error
	code  string
}

func (f *fakeGitHub) Convert(_ context.Context, code string) (*githubapp.Credentials, error) {
	f.code = code
	if f.err != nil {
		return nil, f.err
	}
	return f.creds, nil
}

// setup is what a test varies about a server. The zero value is an un-onboarded
// install with no catalog, which is what most of these tests want.
type setup struct {
	store   *fakeStore
	github  *fakeGitHub
	private string
	public  string
	env     map[string]string

	// catalog is nil unless a test needs the routes that only exist once there
	// is something to serve, which includes the SPA and its assets.
	catalog server.Catalog

	// pages and plugins are what the homepage is declared as and what can be
	// mounted on it. Both nil is the default page with nothing to mount.
	pages        server.Pages
	plugins      server.Plugins
	answers      *answer.Service
	control      *fakeController
	syncs        server.Syncs
	insights     server.Insights
	notes        server.Notes
	context      server.AgentContext
	profile      server.ContextFile
	repositories server.RepositoryFiles
	declarations server.Declarations
	tokens       *proof.Store
}

type fakeController struct {
	synced chan struct{}
}

func (f *fakeController) Sync(context.Context) error {
	f.synced <- struct{}{}
	return nil
}

func (*fakeController) SyncPush(context.Context, controller.Push) error       { return nil }
func (*fakeController) SyncPreview(context.Context, controller.Preview) error { return nil }

func (*fakeController) SharesRepository(context.Context, []string) (bool, error) {
	return false, nil
}

func newServer(t *testing.T, cs *fakeStore, gh *fakeGitHub) http.Handler {
	t.Helper()
	return build(t, setup{store: cs, github: gh})
}

// newServerWithHosts covers the split-host deployment, where GitHub reaches a
// public forwarder and browsers reach a private hostname.
func newServerWithHosts(t *testing.T, private, public string) http.Handler {
	t.Helper()
	return build(t, setup{private: private, public: public})
}

func build(t *testing.T, s setup) http.Handler {
	t.Helper()

	if s.store == nil {
		s.store = &fakeStore{}
	}
	if s.github == nil {
		s.github = &fakeGitHub{}
	}
	if s.private == "" {
		s.private = externalURL
	}

	key, err := vault.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	env := map[string]string{"DUSK_PRIVATE_HOST": s.private, "DUSK_ENCRYPTION_KEY": key}
	if s.public != "" {
		env["DUSK_PUBLIC_HOST"] = s.public
	}
	maps.Copy(env, s.env)
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	srv, err := server.New(server.Options{
		Config:       cfg,
		Credentials:  s.store,
		GitHub:       s.github,
		Catalog:      s.catalog,
		Pages:        s.pages,
		Plugins:      s.plugins,
		Answers:      s.answers,
		Controller:   s.control,
		Syncs:        s.syncs,
		Insights:     s.insights,
		Notes:        s.notes,
		AgentContext: s.context,
		ContextFile:  s.profile,
		Repositories: s.repositories,
		Declarations: s.declarations,
		Tokens:       s.tokens,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv.Handler()
}

// emptyCatalog is a real index rather than a stub: the interface is thirteen
// methods, and a hand-written fake of it rots the moment one is added.
func emptyCatalog(t *testing.T) server.Catalog {
	t.Helper()

	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestHealthAndReadiness(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		onboarded  bool
		wantStatus int
		wantBody   string
	}{
		{name: "health is up before onboarding", path: "/healthz", wantStatus: http.StatusOK, wantBody: "ok"},
		{name: "health is up after onboarding", path: "/healthz", onboarded: true, wantStatus: http.StatusOK, wantBody: "ok"},
		{
			name: "readiness is true before onboarding, and says where to go",
			path: "/readyz", wantStatus: http.StatusOK, wantBody: "/setup",
		},
		{name: "readiness is true once onboarded", path: "/readyz", onboarded: true, wantStatus: http.StatusOK, wantBody: "ready"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &fakeStore{}
			if tt.onboarded {
				cs.creds = sampleCreds()
			}
			rec := get(t, newServer(t, cs, &fakeGitHub{}), tt.path)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

// ADR-0005 registers the App with only the permissions its mode needs, so the
// install screen shows the truth and read-only really is read-only.
func TestADR0005_ManifestRequestsOnlyWhatTheModeNeeds(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		want    map[string]string
		notWant []string
	}{
		{
			name: "read mode grants no write anywhere",
			mode: "read",
			want: map[string]string{"contents": "read", "metadata": "read", "pull_requests": "read"},
		},
		{
			name:    "proposal mode adds pull request write but not contents write",
			mode:    "proposal",
			want:    map[string]string{"contents": "read", "pull_requests": "write"},
			notWant: []string{"contents"},
		},
		{
			name: "write mode asks for contents write",
			mode: "write",
			want: map[string]string{"contents": "write"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, newServer(t, &fakeStore{}, &fakeGitHub{}), "/setup?mode="+tt.mode)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			m := manifestFrom(t, rec.Body.String())

			for field, level := range tt.want {
				if got := m.DefaultPermissions[field]; got != level {
					t.Errorf("permission %q = %q, want %q", field, got, level)
				}
			}
			for _, field := range tt.notWant {
				if got := m.DefaultPermissions[field]; got == "write" {
					t.Errorf("%s mode must not request write on %q", tt.mode, field)
				}
			}
			if tt.mode == "read" {
				for field, level := range m.DefaultPermissions {
					if level != "read" {
						t.Errorf("read mode granted %q on %q, want read only", level, field)
					}
				}
			}
		})
	}
}

// GitHub rejected the first manifest for both of these, and neither is
// reproducible without talking to the real API.
func TestADR0005_ManifestSatisfiesGitHubsManifestRules(t *testing.T) {
	// Events GitHub delivers to every App. Declaring them is invalid.
	implicit := map[string]bool{"installation": true, "installation_repositories": true}

	// Event to the permission GitHub requires for it.
	needs := map[string]string{"push": "contents", "pull_request": "pull_requests"}

	for _, mode := range []string{"read", "proposal", "write"} {
		t.Run(mode, func(t *testing.T) {
			m := manifestFrom(t, get(t, newServer(t, &fakeStore{}, &fakeGitHub{}), "/setup?mode="+mode).Body.String())

			for _, e := range m.DefaultEvents {
				if implicit[e] {
					t.Errorf("event %q is implicit and must not be declared", e)
				}
				perm, known := needs[e]
				if !known {
					t.Errorf("event %q has no known permission mapping, check it against GitHub", e)
					continue
				}
				if _, granted := m.DefaultPermissions[perm]; !granted {
					t.Errorf("event %q requires permission %q, which this mode does not request", e, perm)
				}
			}
		})
	}
}

func TestSetupPagePostsToGitHub(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		wantAction string
	}{
		{name: "personal accounts post to the user settings endpoint", target: "/setup", wantAction: githubapp.CreateURL},
		{name: "an org posts to the org settings endpoint", target: "/setup?org=nerdswhofish", wantAction: githubapp.OrgCreateURL("nerdswhofish")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := get(t, newServer(t, &fakeStore{}, &fakeGitHub{}), tt.target).Body.String()

			if !strings.Contains(body, tt.wantAction) {
				t.Errorf("form should post to %s", tt.wantAction)
			}
			if !strings.Contains(body, `name="manifest"`) {
				t.Error("form is missing the manifest field")
			}
			if !strings.Contains(body, externalURL+"/setup/callback") {
				t.Error("manifest is missing the redirect URL")
			}
			manifest := manifestFrom(t, body)
			setupURL, err := url.Parse(manifest.SetupURL)
			if err != nil {
				t.Fatalf("parse setup URL: %v", err)
			}
			if got := setupURL.Scheme + "://" + setupURL.Host + setupURL.Path; got != externalURL+"/setup/installed" {
				t.Errorf("setup URL = %q, want the post-install page", manifest.SetupURL)
			}
			if setupURL.Query().Get("state") == "" {
				t.Error("setup URL has no one-use state")
			}
		})
	}
}

func TestSetupCallback(t *testing.T) {
	t.Run("a valid callback stores credentials and redirects", func(t *testing.T) {
		cs := &fakeStore{}
		gh := &fakeGitHub{creds: githubCreds()}
		h := newServer(t, cs, gh)

		state := stateFrom(t, get(t, h, "/setup?mode=write").Body.String())
		rec := get(t, h, "/setup/callback?code=abc123&state="+url.QueryEscape(state))

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303. body: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Location"); got != "/setup/done" {
			t.Errorf("Location = %q, want /setup/done", got)
		}
		if gh.code != "abc123" {
			t.Errorf("exchanged code = %q, want abc123", gh.code)
		}
		if cs.savedMode != store.ModeWrite {
			t.Errorf("saved mode = %q, want write", cs.savedMode)
		}
		if cs.creds.PrivateKey.Reveal() != "BEGIN TEST KEY" {
			t.Error("private key was not stored")
		}
	})

	t.Run("a state token cannot be replayed", func(t *testing.T) {
		h := newServer(t, &fakeStore{}, &fakeGitHub{creds: githubCreds()})
		state := stateFrom(t, get(t, h, "/setup").Body.String())

		if rec := get(t, h, "/setup/callback?code=abc&state="+url.QueryEscape(state)); rec.Code != http.StatusSeeOther {
			t.Fatalf("first callback status = %d, want 303", rec.Code)
		}
		if rec := get(t, h, "/setup/callback?code=abc&state="+url.QueryEscape(state)); rec.Code != http.StatusBadRequest {
			t.Errorf("replayed callback status = %d, want 400", rec.Code)
		}
	})
}

func TestSetupCallbackFailures(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		useState   bool
		githubErr  error
		wantStatus int
		wantText   string
	}{
		{name: "no code at all", target: "/setup/callback", wantStatus: http.StatusBadRequest, wantText: "no code"},
		{name: "an unknown state token", target: "/setup/callback?code=abc&state=made-up", wantStatus: http.StatusBadRequest, wantText: "stale"},
		{
			name:   "GitHub refusing the exchange is reported, not swallowed",
			target: "/setup/callback?code=abc", useState: true,
			githubErr: errors.New("410 Gone"), wantStatus: http.StatusBadGateway, wantText: "410 Gone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := &fakeGitHub{creds: githubCreds(), err: tt.githubErr}
			h := newServer(t, &fakeStore{}, gh)

			target := tt.target
			if tt.useState {
				target += "&state=" + url.QueryEscape(stateFrom(t, get(t, h, "/setup").Body.String()))
			}
			rec := get(t, h, target)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tt.wantText) {
				t.Errorf("page should mention %q, got:\n%s", tt.wantText, rec.Body.String())
			}
		})
	}
}

// An unready pod gets no Service endpoints, so /setup becomes unreachable and
// onboarding can never happen. Readiness must not depend on being onboarded.
func TestReadinessDoesNotDeadlockOnboarding(t *testing.T) {
	h := newServer(t, &fakeStore{}, &fakeGitHub{})

	if rec := get(t, h, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("/readyz = %d before onboarding, want 200. An unready pod cannot be onboarded.", rec.Code)
	}
	if rec := get(t, h, "/setup"); rec.Code != http.StatusOK {
		t.Errorf("/setup = %d, want 200", rec.Code)
	}
}

func TestUnknownPathRedirectsToSetupUntilOnboarded(t *testing.T) {
	tests := []struct {
		name       string
		onboarded  bool
		wantStatus int
	}{
		{name: "before onboarding it redirects somewhere useful", wantStatus: http.StatusSeeOther},
		{name: "after onboarding it is a normal 404", onboarded: true, wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &fakeStore{}
			if tt.onboarded {
				cs.creds = sampleCreds()
			}
			if rec := get(t, newServer(t, cs, &fakeGitHub{}), "/entities"); rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestDonePageShowsTheInstallLink(t *testing.T) {
	cs := &fakeStore{creds: sampleCreds()}
	body := get(t, newServer(t, cs, &fakeGitHub{}), "/setup/done").Body.String()

	if !strings.Contains(body, "https://github.com/apps/dusk-example/installations/new") {
		t.Error("done page is missing the install link")
	}
	if !strings.Contains(body, "DUSK_ENCRYPTION_KEY") {
		t.Error("done page should warn that losing the key is unrecoverable")
	}
}

func TestInstallReturnStartsOneInitialSweep(t *testing.T) {
	cs := &fakeStore{}
	gh := &fakeGitHub{creds: githubCreds()}
	control := &fakeController{synced: make(chan struct{}, 2)}
	h := build(t, setup{store: cs, github: gh, control: control})

	setupBody := get(t, h, "/setup").Body.String()
	manifest := manifestFrom(t, setupBody)
	state := stateFrom(t, setupBody)
	callback := "/setup/callback?code=abc&state=" + url.QueryEscape(state)
	if rec := get(t, h, callback); rec.Code != http.StatusSeeOther {
		t.Fatalf("callback = %d, want 303", rec.Code)
	}

	installed, err := url.Parse(manifest.SetupURL)
	if err != nil {
		t.Fatalf("parse setup URL: %v", err)
	}
	rec := get(t, h, installed.RequestURI()+"&installation_id=123")
	if rec.Code != http.StatusOK {
		t.Fatalf("installed page = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Get the first result") || !strings.Contains(rec.Body.String(), "dusk: v1alpha1") {
		t.Error("installed page does not lead to a first catalog result")
	}

	select {
	case <-control.synced:
	case <-time.After(time.Second):
		t.Fatal("post-install page did not start a sweep")
	}

	_ = get(t, h, installed.RequestURI())
	select {
	case <-control.synced:
		t.Fatal("replaying the post-install URL started another sweep")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestInstallReturnBeforeRegistrationStartsOver(t *testing.T) {
	rec := get(t, newServer(t, &fakeStore{}, &fakeGitHub{}), "/setup/installed?state=made-up")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/setup" {
		t.Errorf("installed before registration = %d to %q, want 303 to /setup", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSetupPagesNeverRenderSecrets(t *testing.T) {
	cs := &fakeStore{creds: sampleCreds()}
	h := newServer(t, cs, &fakeGitHub{})

	for _, path := range []string{"/setup/done", "/setup/installed", "/setup"} {
		t.Run(path, func(t *testing.T) {
			body := get(t, h, path).Body.String()
			for _, leak := range []string{"BEGIN TEST KEY", "hook-secret", "client-secret"} {
				if strings.Contains(body, leak) {
					t.Errorf("%s leaked %q", path, leak)
				}
			}
		})
	}
}

func githubCreds() *githubapp.Credentials {
	return &githubapp.Credentials{
		ID: 999, Slug: "dusk-example", Name: "Dusk",
		HTMLURL: "https://github.com/apps/dusk-example",
		PEM:     "BEGIN TEST KEY", WebhookSecret: webhookSecret, ClientSecret: "client-secret",
	}
}

func sampleCreds() *store.Credentials {
	return store.FromGitHub(githubCreds(), store.ModeProposal)
}

// stateFrom pulls the CSRF token out of the GitHub form's action.
func stateFrom(t *testing.T, body string) string {
	t.Helper()
	action := attrValue(t, body, `action="`, "state=")
	return action[strings.Index(action, "state=")+len("state="):]
}

// manifestFrom decodes the manifest the browser would POST to GitHub, which is
// what actually determines the permissions on the install screen.
func manifestFrom(t *testing.T, body string) githubapp.Manifest {
	t.Helper()
	raw := attrValue(t, body, `name="manifest" value='`, "")

	var m githubapp.Manifest
	if err := json.Unmarshal([]byte(html.UnescapeString(raw)), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v\n%s", err, raw)
	}
	return m
}

// attrValue returns the first attribute after prefix that contains must.
func attrValue(t *testing.T, body, prefix, must string) string {
	t.Helper()
	rest := body
	for {
		i := strings.Index(rest, prefix)
		if i < 0 {
			t.Fatalf("no attribute %q containing %q", prefix, must)
		}
		rest = rest[i+len(prefix):]
		close := strings.IndexAny(rest, `"'`)
		if close < 0 {
			t.Fatalf("unterminated attribute %q", prefix)
		}
		if value := rest[:close]; must == "" || strings.Contains(value, must) {
			return value
		}
		rest = rest[close:]
	}
}
