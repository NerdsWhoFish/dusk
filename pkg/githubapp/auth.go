package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/FetchHQ/dusk/pkg/secret"
)

// APIVersion pins the REST API so a GitHub change cannot silently alter
// responses under a running Dusk.
const APIVersion = "2022-11-28"

// App is the identity Dusk authenticates as: an App id and its private key.
type App struct {
	ID         int64
	PrivateKey secret.String
}

// jwtLifetime stays under GitHub's ten minute ceiling.
const jwtLifetime = 9 * time.Minute

// jwtBackdate absorbs clock skew between here and GitHub, which rejects a
// token whose iat is in its future.
const jwtBackdate = 60 * time.Second

// JWT mints a short-lived assertion proving possession of the App's key. It is
// the credential for App-level calls, and is exchanged for an installation
// token to reach any repository.
func (a App) JWT(now time.Time) (secret.String, error) {
	key, err := a.rsaKey()
	if err != nil {
		return secret.String{}, err
	}

	claims := map[string]any{
		"iat": now.Add(-jwtBackdate).Unix(),
		"exp": now.Add(jwtLifetime).Unix(),
		"iss": a.ID,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return secret.String{}, fmt.Errorf("githubapp: encode claims: %w", err)
	}

	signing := encodeSegment([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + encodeSegment(payload)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return secret.String{}, fmt.Errorf("githubapp: sign assertion: %w", err)
	}
	return secret.New(signing + "." + encodeSegment(signature)), nil
}

func encodeSegment(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// rsaKey accepts both PEM encodings, because GitHub issues PKCS#1 while a key
// round-tripped through other tooling commonly arrives as PKCS#8.
func (a App) rsaKey() (*rsa.PrivateKey, error) {
	if a.PrivateKey.IsZero() {
		return nil, errors.New("githubapp: no private key, Dusk is not onboarded")
	}
	block, _ := pem.Decode([]byte(a.PrivateKey.Reveal()))
	if block == nil {
		return nil, errors.New("githubapp: private key is not PEM encoded")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("githubapp: parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("githubapp: private key is %T, want RSA", parsed)
	}
	return key, nil
}

// InstallationToken is a short-lived credential scoped to one installation,
// which is the only thing that can actually read a repository.
type InstallationToken struct {
	Token     secret.String `json:"token"`
	ExpiresAt time.Time     `json:"expires_at"`
}

// InstallationToken exchanges an App assertion for an installation token.
func (c *Client) InstallationToken(ctx context.Context, app App, installationID int64) (*InstallationToken, error) {
	assertion, err := app.JWT(time.Now())
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL(), installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("githubapp: build token request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	req.Header.Set("Authorization", "Bearer "+assertion.Reveal())

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: request installation token: %w", err)
	}
	c.observe(resp)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("githubapp: installation %d token: %w", installationID, statusError(resp))
	}

	token := &InstallationToken{}
	if err := json.NewDecoder(resp.Body).Decode(token); err != nil {
		return nil, fmt.Errorf("githubapp: decode installation token: %w", err)
	}
	if token.Token.IsZero() {
		return nil, errors.New("githubapp: GitHub returned an empty installation token")
	}
	return token, nil
}

// statusError reports an unexpected status with GitHub's own message, which
// usually says exactly which permission is missing.
func statusError(resp *http.Response) error {
	if limited := rateLimited(resp); limited != nil {
		return limited
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := bytes.TrimSpace(body)
	if len(message) == 0 {
		return fmt.Errorf("github returned %s", resp.Status)
	}
	return fmt.Errorf("github returned %s: %s", resp.Status, message)
}

// Tokens mints installation tokens and reuses one until it is close to expiry,
// because a reconcile reads one file per entity and each would otherwise cost
// an extra round trip and a signature.
type Tokens struct {
	Client *Client
	App    App

	// Now exists so a test can age a token out without waiting an hour.
	Now func() time.Time

	mu     sync.Mutex
	cached map[int64]*InstallationToken
}

// renewBefore refreshes early, so a token cannot expire midway through a
// reconcile that started with a valid one.
const renewBefore = 5 * time.Minute

// Token returns a usable token for the installation, minting one if the
// cached token is missing or close to expiring.
func (t *Tokens) Token(ctx context.Context, installationID int64) (secret.String, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if cached, ok := t.cached[installationID]; ok && t.now().Add(renewBefore).Before(cached.ExpiresAt) {
		return cached.Token, nil
	}

	minted, err := t.Client.InstallationToken(ctx, t.App, installationID)
	if err != nil {
		return secret.String{}, err
	}
	if t.cached == nil {
		t.cached = make(map[int64]*InstallationToken)
	}
	t.cached[installationID] = minted
	return minted.Token, nil
}

func (t *Tokens) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}
