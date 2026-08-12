package access

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/FetchHQ/dusk/pkg/secret"
)

// IdentityCookie holds a signed GitHub identity. It is separate from the
// shared-token session because the two answer different questions: one says
// "you know the password", the other says "you are this person".
const IdentityCookie = "dusk_identity"

// GitHub is the slice of GitHub an OAuth sign-in needs.
type GitHub interface {
	// Exchange turns an OAuth callback code into a user access token.
	Exchange(ctx context.Context, code string) (secret.String, error)

	// Viewer returns who the token belongs to.
	Viewer(ctx context.Context, token secret.String) (string, error)

	// Readable lists the repositories that person can read, as owner/name.
	Readable(ctx context.Context, token secret.String) ([]string, error)
}

// Identity is a signed-in person and what they may see.
type Identity struct {
	Login string

	// Readable is the set of repositories GitHub says they can read, which is
	// the whole permission model: ADR-0012 derives authorization rather than
	// configuring it, so there is nothing here to administer or to drift.
	Readable []string

	Expires time.Time
}

// MaySee reports whether this person may see what a repository contributed.
func (i Identity) MaySee(repository string) bool {
	return slices.Contains(i.Readable, repository)
}

// OAuth signs people in with GitHub.
type OAuth struct {
	ClientID     string
	ClientSecret secret.String
	Callback     string
	GitHub       GitHub

	// Policy signs the identity cookie, reusing the session signing key so
	// there is one secret to rotate rather than two.
	Policy *Policy

	mu       sync.Mutex
	pending  map[string]time.Time
	sessions map[string]Identity
}

// Configured reports whether sign-in with GitHub is available.
func (o *OAuth) Configured() bool {
	return o != nil && o.ClientID != "" && !o.ClientSecret.IsZero() && o.GitHub != nil
}

// Begin starts the flow, returning the URL to send the browser to.
func (o *OAuth) Begin() (string, error) {
	state, err := randomState()
	if err != nil {
		return "", err
	}

	o.mu.Lock()
	if o.pending == nil {
		o.pending = map[string]time.Time{}
	}
	o.pending[state] = o.now().Add(10 * time.Minute)
	o.sweep()
	o.mu.Unlock()

	query := url.Values{
		"client_id":    {o.ClientID},
		"redirect_uri": {o.Callback},
		"scope":        {"read:user repo"},
		"state":        {state},
	}
	return "https://github.com/login/oauth/authorize?" + query.Encode(), nil
}

// Complete finishes the flow and returns a session id. The state is single
// use, or a captured callback URL could be replayed to sign somebody in again.
func (o *OAuth) Complete(ctx context.Context, code, state string) (string, Identity, error) {
	if !o.consume(state) {
		return "", Identity{}, fmt.Errorf("access: this sign-in did not start here, or it took too long")
	}

	token, err := o.GitHub.Exchange(ctx, code)
	if err != nil {
		return "", Identity{}, err
	}
	login, err := o.GitHub.Viewer(ctx, token)
	if err != nil {
		return "", Identity{}, err
	}
	readable, err := o.GitHub.Readable(ctx, token)
	if err != nil {
		return "", Identity{}, err
	}

	id, err := randomState()
	if err != nil {
		return "", Identity{}, err
	}
	identity := Identity{Login: login, Readable: readable, Expires: o.now().Add(SessionLife)}

	o.mu.Lock()
	if o.sessions == nil {
		o.sessions = map[string]Identity{}
	}
	o.sessions[id] = identity
	o.mu.Unlock()

	return id, identity, nil
}

// Identify returns who a request is, if anybody. Sessions live in memory, so a
// restart signs everybody out: persisting them would mean keeping a second,
// staler copy of GitHub's answer about who can see what.
func (o *OAuth) Identify(r *http.Request) (Identity, bool) {
	cookie, err := r.Cookie(IdentityCookie)
	if err != nil {
		return Identity{}, false
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	identity, ok := o.sessions[cookie.Value]
	if !ok || o.now().After(identity.Expires) {
		delete(o.sessions, cookie.Value)
		return Identity{}, false
	}
	return identity, true
}

// SetIdentity writes the identity cookie.
func (o *OAuth) SetIdentity(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     IdentityCookie,
		Value:    id,
		Path:     "/",
		Expires:  o.now().Add(SessionLife),
		HttpOnly: true,
		Secure:   o.Policy != nil && o.Policy.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearIdentity signs a person out.
func (o *OAuth) ClearIdentity(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(IdentityCookie); err == nil {
		o.mu.Lock()
		delete(o.sessions, cookie.Value)
		o.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: IdentityCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: o.Policy != nil && o.Policy.secure, SameSite: http.SameSiteLaxMode,
	})
}

func (o *OAuth) consume(state string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	expires, ok := o.pending[state]
	delete(o.pending, state)
	return ok && o.now().Before(expires)
}

// sweep drops expired states. Called under the lock, on a map that only grows
// when somebody starts a sign-in and abandons it.
func (o *OAuth) sweep() {
	now := o.now()
	for state, expires := range o.pending {
		if now.After(expires) {
			delete(o.pending, state)
		}
	}
}

func (o *OAuth) now() time.Time {
	if o.Policy != nil && o.Policy.now != nil {
		return o.Policy.now()
	}
	return time.Now()
}

func randomState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("access: generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Client talks to GitHub's OAuth and REST endpoints.
type Client struct {
	ClientID     string
	ClientSecret secret.String
	HTTP         *http.Client
	BaseURL      string
	OAuthURL     string
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) api() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return "https://api.github.com"
}

func (c *Client) oauth() string {
	if c.OAuthURL != "" {
		return strings.TrimSuffix(c.OAuthURL, "/")
	}
	return "https://github.com"
}

// Exchange turns a callback code into a user access token.
func (c *Client) Exchange(ctx context.Context, code string) (secret.String, error) {
	form := url.Values{
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret.Reveal()},
		"code":          {code},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.oauth()+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return secret.String{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return secret.String{}, fmt.Errorf("access: exchange code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return secret.String{}, fmt.Errorf("access: decode token response: %w", err)
	}
	if body.AccessToken == "" {
		return secret.String{}, fmt.Errorf("access: GitHub refused the sign-in: %s", body.Error)
	}
	return secret.New(body.AccessToken), nil
}

// Viewer returns the signed-in person's login.
func (c *Client) Viewer(ctx context.Context, token secret.String) (string, error) {
	var body struct {
		Login string `json:"login"`
	}
	if err := c.get(ctx, token, "/user", &body); err != nil {
		return "", err
	}
	return body.Login, nil
}

// Readable lists every repository the person can read, which is the whole
// permission model. It pages, because somebody with a hundred repositories
// would otherwise silently see only the first thirty.
func (c *Client) Readable(ctx context.Context, token secret.String) ([]string, error) {
	var readable []string

	for page := 1; page <= 20; page++ {
		var body []struct {
			FullName string `json:"full_name"`
		}
		target := fmt.Sprintf("/user/repos?per_page=100&page=%d", page)
		if err := c.get(ctx, token, target, &body); err != nil {
			return nil, err
		}
		for _, repo := range body {
			readable = append(readable, repo.FullName)
		}
		if len(body) < 100 {
			break
		}
	}
	return readable, nil
}

func (c *Client) get(ctx context.Context, token secret.String, target string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api()+target, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token.Reveal())

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("access: %s: %w", target, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("access: %s: github returned %s", target, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(into)
}
