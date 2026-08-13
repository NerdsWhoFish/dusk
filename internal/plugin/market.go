package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Prefix is the whole registry. A repository named this way in an allowlisted
// org is a plugin, which is why there is no list to submit to (ADR-0042).
const Prefix = "dusk-plugin-"

// DefaultOrgs is where Dusk looks when nothing says otherwise. NerdsWhoFish is
// first rather than special: an operator can remove it.
var DefaultOrgs = []string{"NerdsWhoFish"}

// Listing is one plugin the marketplace offers.
type Listing struct {
	// ID is the name after the prefix, and is what the plugin is called
	// everywhere else: the scope, the socket, the directory.
	ID string `json:"id"`

	Org         string `json:"org"`
	Repository  string `json:"repository"`
	Description string `json:"description"`
	URL         string `json:"url"`

	// Version is the latest release, empty when the repository has none. A
	// plugin with no release cannot be installed, and saying so beats hiding it.
	Version string `json:"version,omitempty"`
}

// Market lists plugins across the orgs an operator trusts.
type Market struct {
	Orgs []string
	HTTP *http.Client

	// BaseURL is GitHub's API, overridable for tests.
	BaseURL string

	// Token takes GitHub's limit from 60 requests an hour to 5,000. It is the
	// App's own installation token, not a second credential: any authenticated
	// identity raises the limit. A function because such a token expires.
	Token func(context.Context) (string, error)

	// TTL is how long a listing is reused. Browsing the marketplace must not
	// cost API budget per page view, which is the ungated periodic traffic
	// ADR-0017 names as the failure mode.
	TTL time.Duration

	mu     sync.Mutex
	cached []Listing
	fresh  time.Time
}

// cacheFor defaults the TTL. A day, because browsing the marketplace should
// cost nothing and a plugin release is not news that has to arrive within the
// hour. Refresh is how somebody asks for it sooner.
const cacheFor = 24 * time.Hour

func (m *Market) ttl() time.Duration {
	if m.TTL > 0 {
		return m.TTL
	}
	return cacheFor
}

func (m *Market) orgs() []string {
	if len(m.Orgs) > 0 {
		return m.Orgs
	}
	return DefaultOrgs
}

func (m *Market) httpClient() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// token asks for an App token and shrugs if there is none. An unauthenticated
// listing still works, just against a much smaller budget, so failing to mint
// one must not take the marketplace down.
func (m *Market) token(ctx context.Context) string {
	if m.Token == nil {
		return ""
	}
	token, err := m.Token(ctx)
	if err != nil {
		return ""
	}
	return token
}

func (m *Market) api() string {
	if m.BaseURL != "" {
		return strings.TrimSuffix(m.BaseURL, "/")
	}
	return "https://api.github.com"
}

type repoJSON struct {
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	HTMLURL     string `json:"html_url"`
	Archived    bool   `json:"archived"`
}

type releaseJSON struct {
	TagName    string      `json:"tag_name"`
	Draft      bool        `json:"draft"`
	Prerelease bool        `json:"prerelease"`
	Assets     []assetJSON `json:"assets"`
}

type assetJSON struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// List returns every plugin across the configured orgs. The prefix filters
// before any release is fetched, so the request count is bounded by how many
// plugins an org publishes rather than how many repositories it has.
func (m *Market) List(ctx context.Context) ([]Listing, error) {
	m.mu.Lock()
	if m.cached != nil && time.Since(m.fresh) < m.ttl() {
		cached := m.cached
		m.mu.Unlock()
		return cached, nil
	}
	m.mu.Unlock()

	listings, err := m.fetch(ctx)
	if err != nil {
		// Stale beats empty. A rate limit should not make the marketplace look
		// like an org that publishes nothing.
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.cached != nil {
			return m.cached, err
		}
		return nil, err
	}

	m.mu.Lock()
	m.cached, m.fresh = listings, time.Now()
	m.mu.Unlock()
	return listings, nil
}

// Refresh fetches regardless of the cache, for somebody asking whether there
// is an update rather than waiting a day to be told.
func (m *Market) Refresh(ctx context.Context) ([]Listing, error) {
	listings, err := m.fetch(ctx)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.cached, m.fresh = listings, time.Now()
	m.mu.Unlock()
	return listings, nil
}

// Checked is when the listing was last fetched, so the UI can say how old what
// it is showing actually is.
func (m *Market) Checked() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fresh
}

func (m *Market) fetch(ctx context.Context) ([]Listing, error) {
	var listings []Listing

	for _, org := range m.orgs() {
		repos, err := m.repositories(ctx, org)
		if err != nil {
			return nil, err
		}

		for _, repo := range repos {
			if repo.Archived || !strings.HasPrefix(repo.Name, Prefix) {
				continue
			}

			// The prefix alone offers `dusk-plugin-sdk`, which is the contract
			// every plugin compiles against rather than a plugin. Publishing
			// something installable is the test, not a list of names to skip.
			release, err := m.latest(ctx, repo.FullName)
			if err != nil || !hasAsset(release, strings.TrimPrefix(repo.Name, Prefix)) {
				continue
			}

			listings = append(listings, Listing{
				ID:          strings.TrimPrefix(repo.Name, Prefix),
				Org:         org,
				Repository:  repo.FullName,
				Description: repo.Description,
				URL:         repo.HTMLURL,
				Version:     release.TagName,
			})
		}
	}
	return listings, nil
}

func (m *Market) repositories(ctx context.Context, org string) ([]repoJSON, error) {
	var repos []repoJSON
	for page := 1; page <= 10; page++ {
		var body []repoJSON
		target := fmt.Sprintf("/orgs/%s/repos?per_page=100&page=%d", org, page)
		if err := m.get(ctx, target, &body); err != nil {
			return nil, fmt.Errorf("plugin: list %s: %w", org, err)
		}

		repos = append(repos, body...)
		if len(body) < 100 {
			break
		}
	}
	return repos, nil
}

func (m *Market) latest(ctx context.Context, repository string) (*releaseJSON, error) {
	var release releaseJSON
	if err := m.get(ctx, "/repos/"+repository+"/releases/latest", &release); err != nil {
		return nil, err
	}
	if release.Draft {
		return nil, fmt.Errorf("plugin: %s has only a draft release", repository)
	}
	return &release, nil
}

func (m *Market) get(ctx context.Context, target string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.api()+target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if token := m.token(ctx); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := m.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	// A rate limit is the expected failure here and reads as a permissions
	// problem, so it says which it is and what fixes it.
	if response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0" {
		unauthenticated := ""
		if m.token(ctx) == "" {
			unauthenticated = ". Dusk could not mint an App token, so the request was anonymous and limited to 60 an hour"
		}
		return fmt.Errorf("github's rate limit is spent, and resets at %s%s",
			resetAt(response.Header.Get("X-RateLimit-Reset")), unauthenticated)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("github answered %s for %s", response.Status, target)
	}
	return json.NewDecoder(response.Body).Decode(into)
}

// hasAsset reports whether a release carries a binary for this machine. A
// plugin that builds for other platforms is not offered here, because offering
// something that cannot be installed is worse than not listing it.
func hasAsset(release *releaseJSON, id string) bool {
	wanted := assetName(id)
	for _, asset := range release.Assets {
		if asset.Name == wanted {
			return true
		}
	}
	return false
}

// resetAt turns GitHub's epoch seconds into a time somebody can act on.
func resetAt(header string) string {
	seconds, err := strconv.ParseInt(header, 10, 64)
	if err != nil {
		return "an unknown time"
	}
	return time.Unix(seconds, 0).Format(time.Kitchen)
}

// assetName is what a release calls the binary for this machine. GoReleaser's
// default naming is what the plugin release convention produces, so the host
// can find the right artifact without the plugin describing itself first.
func assetName(id string) string {
	return fmt.Sprintf("%s%s_%s_%s.tar.gz", Prefix, id, runtime.GOOS, runtime.GOARCH)
}
