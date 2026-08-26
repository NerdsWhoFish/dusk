package access_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/access"
	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

type fakeGitHub struct {
	token    secret.String
	login    string
	readable []string
	err      error
}

func (f *fakeGitHub) Exchange(context.Context, string) (secret.String, error) {
	return f.token, f.err
}

func (f *fakeGitHub) Viewer(context.Context, secret.String) (string, error) {
	return f.login, f.err
}

func (f *fakeGitHub) Readable(context.Context, secret.String) ([]string, error) {
	return f.readable, f.err
}

func source(id, clientSecret string) access.CredentialSource {
	return func() (string, secret.String) { return id, secret.New(clientSecret) }
}

func TestOAuthConfigured(t *testing.T) {
	for _, test := range []struct {
		name string
		auth *access.OAuth
		want bool
	}{
		{"nil", nil, false},
		{"no source", &access.OAuth{GitHub: &fakeGitHub{}}, false},
		{
			"no github",
			&access.OAuth{Credentials: source("id", "shh")},
			false,
		},
		{
			"secret without id",
			&access.OAuth{Credentials: source("", "shh"), GitHub: &fakeGitHub{}},
			false,
		},
		{
			"id without secret",
			&access.OAuth{Credentials: source("id", ""), GitHub: &fakeGitHub{}},
			false,
		},
		{
			"both",
			&access.OAuth{Credentials: source("id", "shh"), GitHub: &fakeGitHub{}},
			true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.auth.Configured(); got != test.want {
				t.Errorf("Configured() = %v, want %v", got, test.want)
			}
		})
	}
}

// The App is registered through /setup, long after the process started. Reading
// credentials once at construction would leave sign-in dark until a restart,
// which is the bug that kept this path unreachable in the first place.
func TestOAuthPicksUpAnAppRegisteredAfterBoot(t *testing.T) {
	var clientID string
	auth := &access.OAuth{
		GitHub:      &fakeGitHub{},
		Credentials: func() (string, secret.String) { return clientID, secret.New("shh") },
	}

	if auth.Configured() {
		t.Fatal("sign-in offered before any app was registered")
	}

	clientID = "Iv1.registered"

	if !auth.Configured() {
		t.Fatal("sign-in still unavailable after an app was registered")
	}

	target, err := auth.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("Begin returned %q: %v", target, err)
	}
	if got := parsed.Query().Get("client_id"); got != clientID {
		t.Errorf("client_id = %q, want %q", got, clientID)
	}
}

func TestOAuthBeginAddressesGitHub(t *testing.T) {
	auth := &access.OAuth{
		Credentials: source("id", "shh"),
		Callback:    "https://dusk.example.com/auth/callback",
		GitHub:      &fakeGitHub{},
	}

	target, err := auth.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !strings.HasPrefix(target, "https://github.com/login/oauth/authorize?") {
		t.Fatalf("Begin sent the browser to %q", target)
	}

	query, err := url.ParseQuery(strings.SplitN(target, "?", 2)[1])
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if got := query.Get("redirect_uri"); got != auth.Callback {
		t.Errorf("redirect_uri = %q, want %q", got, auth.Callback)
	}
	if query.Get("state") == "" {
		t.Error("no state, so the callback cannot be tied to this request")
	}
}

// A state is a one-shot. Without that, a callback URL captured from history or
// a proxy log signs its holder in again whenever they replay it.
func TestOAuthRefusesAReplayedState(t *testing.T) {
	auth := &access.OAuth{
		Credentials: source("id", "shh"),
		GitHub:      &fakeGitHub{login: "joey", readable: []string{"acme/infra"}},
	}

	target, err := auth.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	state := parsed.Query().Get("state")

	if _, _, err := auth.Complete(t.Context(), "code", state); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if _, _, err := auth.Complete(t.Context(), "code", state); err == nil {
		t.Error("a replayed state signed somebody in a second time")
	}
}

func TestOAuthRefusesAStateItNeverIssued(t *testing.T) {
	auth := &access.OAuth{
		Credentials: source("id", "shh"),
		GitHub:      &fakeGitHub{login: "joey"},
	}

	if _, _, err := auth.Complete(t.Context(), "code", "invented"); err == nil {
		t.Error("a state Dusk never issued was accepted")
	}
}

// Signing in has to be a way in. Complete used to set an identity cookie that
// no gate consulted, so a person finished the GitHub round trip and was
// redirected straight back to the login page holding a valid identity.
func TestASignedInPersonIsLetPastTheGate(t *testing.T) {
	policy := access.New(secret.New("shared"), false, false)
	auth := &access.OAuth{
		Credentials: source("id", "shh"),
		Policy:      policy,
		GitHub:      &fakeGitHub{login: "joey", readable: []string{"acme/infra"}},
	}
	policy.Recognize(auth)

	guarded := policy.Browsers(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Nobody signed in yet, so the gate should still turn a browser away.
	cold := httptest.NewRecorder()
	guarded.ServeHTTP(cold, httptest.NewRequest(http.MethodGet, "/", nil))
	if cold.Code != http.StatusSeeOther {
		t.Fatalf("an anonymous browser got %d, want a redirect to the login page", cold.Code)
	}

	target, err := auth.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	signIn := httptest.NewRecorder()
	id, _, err := auth.Complete(t.Context(), "code", parsed.Query().Get("state"))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	auth.SetIdentity(signIn, id)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, cookie := range signIn.Result().Cookies() {
		request.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, request)
	if rec.Code != http.StatusOK {
		t.Errorf("a signed-in person got %d, want to be let through", rec.Code)
	}
}

func TestOAuthCompleteAdmitsAViewerKnownToTheInstallation(t *testing.T) {
	auth := &access.OAuth{
		Credentials: source("id", "shh"),
		GitHub: &fakeGitHub{
			login:    "joey",
			readable: []string{"acme/infra", "acme/web"},
		},
	}

	target, err := auth.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	session, identity, err := auth.Complete(t.Context(), "code", parsed.Query().Get("state"))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if session == "" {
		t.Error("no session id, so the browser has nothing to hold")
	}
	if identity.Login != "joey" {
		t.Errorf("Login = %q", identity.Login)
	}
	if !identity.MaySee("acme/infra") {
		t.Error("a readable repository was not visible")
	}
	if identity.MaySee("acme/private") {
		t.Error("a repository GitHub did not list was visible")
	}
}

func TestOAuthRefusesAViewerOutsideTheInstallation(t *testing.T) {
	auth := &access.OAuth{
		Credentials: source("id", "shh"),
		GitHub:      &fakeGitHub{login: "stranger"},
	}

	target, err := auth.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if _, _, err := auth.Complete(t.Context(), "code", parsed.Query().Get("state")); err == nil {
		t.Fatal("a GitHub identity outside the installation was admitted")
	}
}

func TestClearIdentityEndsTheGitHubSession(t *testing.T) {
	auth := &access.OAuth{
		Credentials: source("id", "shh"),
		GitHub:      &fakeGitHub{login: "joey", readable: []string{"acme/infra"}},
	}
	target, err := auth.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	id, _, err := auth.Complete(t.Context(), "code", parsed.Query().Get("state"))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	signedIn := httptest.NewRecorder()
	auth.SetIdentity(signedIn, id)
	cookie := signedIn.Result().Cookies()[0]
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(cookie)

	signedOut := httptest.NewRecorder()
	auth.ClearIdentity(signedOut, request)
	if _, ok := auth.Identify(request); ok {
		t.Fatal("the server retained a cleared GitHub session")
	}
	cleared := signedOut.Result().Cookies()[0]
	if cleared.Name != access.IdentityCookie || cleared.MaxAge >= 0 {
		t.Fatalf("cleared cookie = %+v", cleared)
	}
}
