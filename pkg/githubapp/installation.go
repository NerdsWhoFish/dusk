package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// pageSize is GitHub's maximum, so a typical install is one request.
const pageSize = 100

// Installation is one place the App is installed. Listing them is how Dusk
// discovers what it may read, rather than being told in configuration.
type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}

// RepositoryRef names a repository and the branch it is tracked at.
type RepositoryRef struct {
	Owner         string `json:"-"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`

	Owns struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// Slug is the owner/name form used in logs and errors.
func (r RepositoryRef) Slug() string { return r.Owner + "/" + r.Name }

// Installations lists everywhere the App is installed. It is an App-level call
// authenticated with an assertion, because an installation token by definition
// cannot see other installations.
func (c *Client) Installations(ctx context.Context, app App) ([]Installation, error) {
	assertion, err := app.JWT(time.Now())
	if err != nil {
		return nil, err
	}

	var all []Installation
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/app/installations?per_page=%d&page=%d", c.baseURL(), pageSize, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("githubapp: build installations request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", APIVersion)
		req.Header.Set("Authorization", "Bearer "+assertion.Reveal())

		batch, err := decodePage[Installation](c.httpClient(), req, "list installations")
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < pageSize {
			return all, nil
		}
	}
}

// Install is one installation, and the handle through which its repositories
// are reached. It exists so a caller wires the client, the token cache, and the
// installation id together once.
type Install struct {
	Client *Client
	Tokens *Tokens
	ID     int64
}

// Repositories lists what the installation grants access to.
func (i *Install) Repositories(ctx context.Context) ([]RepositoryRef, error) {
	token, err := i.Tokens.Token(ctx, i.ID)
	if err != nil {
		return nil, err
	}

	var all []RepositoryRef
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/installation/repositories?per_page=%d&page=%d", i.Client.baseURL(), pageSize, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("githubapp: build repositories request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", APIVersion)
		req.Header.Set("Authorization", "Bearer "+token.Reveal())

		batch, err := decodeRepositories(i.Client.httpClient(), req)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < pageSize {
			return all, nil
		}
	}
}

// Repository returns a reader for one repository in this installation.
func (i *Install) Repository(owner, name string) *Repository {
	return &Repository{
		Client:         i.Client,
		Tokens:         i.Tokens,
		InstallationID: i.ID,
		Owner:          owner,
		Name:           name,
	}
}

// decodeRepositories unwraps the envelope this one endpoint uses, unlike the
// bare arrays the rest of the API returns.
func decodeRepositories(client *http.Client, req *http.Request) ([]RepositoryRef, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: list repositories: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: list repositories: %w", statusError(resp))
	}

	var envelope struct {
		Repositories []RepositoryRef `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("githubapp: decode repositories: %w", err)
	}

	for i := range envelope.Repositories {
		envelope.Repositories[i].Owner = envelope.Repositories[i].Owns.Login
	}
	return envelope.Repositories, nil
}

func decodePage[T any](client *http.Client, req *http.Request, what string) ([]T, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: %s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: %s: %w", what, statusError(resp))
	}

	var page []T
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("githubapp: decode %s: %w", what, err)
	}
	return page, nil
}
