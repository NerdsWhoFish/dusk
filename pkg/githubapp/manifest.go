// Package githubapp implements the GitHub App Manifest flow, which lets a
// service register its own App instead of walking a user through settings
// pages, private keys, and webhook secrets by hand.
//
// The flow has three steps and must complete within an hour:
//
//  1. The browser POSTs a manifest to GitHub. This cannot be done server side.
//  2. GitHub redirects back with a temporary code.
//  3. The service exchanges that code for credentials.
package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CreateURL is where the browser POSTs a manifest for a personal account.
const CreateURL = "https://github.com/settings/apps/new"

// OrgCreateURL is the equivalent for an organization. An org-owned App
// requires org admin, so this fails for users who do not have it.
func OrgCreateURL(org string) string {
	return "https://github.com/organizations/" + url.PathEscape(org) + "/settings/apps/new"
}

// HookAttributes configures where GitHub delivers webhooks.
type HookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// Manifest is the App registration GitHub renders for the user to accept.
// RedirectURL must be browser-reachable, which is why the service must know
// its own external URL before setup can start.
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	HookAttributes     HookAttributes    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	CallbackURLs       []string          `json:"callback_urls,omitempty"`
	Description        string            `json:"description,omitempty"`
	Public             bool              `json:"public"`
	DefaultEvents      []string          `json:"default_events,omitempty"`
	DefaultPermissions map[string]string `json:"default_permissions,omitempty"`
}

// Credentials are what the code exchange returns. The PEM and WebhookSecret
// are secrets and must never be logged or rendered.
type Credentials struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	NodeID        string `json:"node_id"`
	Name          string `json:"name"`
	HTMLURL       string `json:"html_url"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
}

// InstallURL is where the user goes to install the App onto repositories.
// Registration alone grants nothing; installation is what grants access.
func (c Credentials) InstallURL() string {
	if c.HTMLURL == "" {
		return ""
	}
	return strings.TrimSuffix(c.HTMLURL, "/") + "/installations/new"
}

// Client exchanges manifest codes for credentials.
type Client struct {
	// HTTP defaults to a client with a timeout. The zero value of http.Client
	// has none, which turns a hung GitHub into a hung setup page.
	HTTP *http.Client

	// BaseURL allows pointing at GitHub Enterprise, or at a test server.
	BaseURL string
}

const defaultBaseURL = "https://api.github.com"

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return defaultBaseURL
}

// Convert exchanges a temporary manifest code for App credentials.
// The code is single use and expires an hour into the flow.
func (c *Client) Convert(ctx context.Context, code string) (*Credentials, error) {
	if code == "" {
		return nil, fmt.Errorf("githubapp: empty code")
	}

	endpoint := c.baseURL() + "/app-manifests/" + url.PathEscape(code) + "/conversions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("githubapp: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: exchange code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: exchange code: %s (the code is single use and expires one hour after setup begins)", resp.Status)
	}

	var creds Credentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, fmt.Errorf("githubapp: decode credentials: %w", err)
	}
	if creds.ID == 0 || creds.PEM == "" {
		return nil, fmt.Errorf("githubapp: exchange returned no usable credentials")
	}
	return &creds, nil
}

// ManifestJSON renders a manifest for embedding in the browser form.
func ManifestJSON(m Manifest) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(m); err != nil {
		return "", fmt.Errorf("githubapp: encode manifest: %w", err)
	}
	return strings.TrimSpace(buf.String()), nil
}
