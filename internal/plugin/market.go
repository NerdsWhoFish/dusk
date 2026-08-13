package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
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

	// Token is optional. Unauthenticated GitHub allows 60 requests an hour per
	// address, which one refresh across a few orgs can exhaust.
	Token string
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
	if m.Token != "" {
		request.Header.Set("Authorization", "Bearer "+m.Token)
	}

	response, err := m.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

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

// assetName is what a release calls the binary for this machine. GoReleaser's
// default naming is what the plugin release convention produces, so the host
// can find the right artifact without the plugin describing itself first.
func assetName(id string) string {
	return fmt.Sprintf("%s%s_%s_%s.tar.gz", Prefix, id, runtime.GOOS, runtime.GOARCH)
}
