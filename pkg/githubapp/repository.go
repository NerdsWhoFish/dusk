package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Repository reads and writes one repository as an installed App. Reads take
// the whole tree in one request (see tarball.go); the single-file calls that
// remain belong to the write path, which needs a blob SHA to commit against.
type Repository struct {
	Client         *Client
	Tokens         *Tokens
	InstallationID int64
	Owner          string
	Name           string
}

// Resolve returns the commit a ref points at.
func (r *Repository) Resolve(ctx context.Context, gitRef string) (string, error) {
	resp, err := r.get(ctx, "/commits/"+escapePath(gitRef), "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("githubapp: resolve %s at %q: %w", r.slug(), gitRef, statusError(resp))
	}

	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commit); err != nil {
		return "", fmt.Errorf("githubapp: decode commit for %q: %w", gitRef, err)
	}
	if commit.SHA == "" {
		return "", fmt.Errorf("githubapp: %s has no commit for %q", r.slug(), gitRef)
	}
	return commit.SHA, nil
}

func (r *Repository) get(ctx context.Context, target, accept string) (*http.Response, error) {
	token, err := r.Tokens.Token(ctx, r.InstallationID)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/repos/%s/%s%s", r.Client.baseURL(), escapePath(r.Owner), escapePath(r.Name), target)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("githubapp: build request for %s: %w", r.slug(), err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	req.Header.Set("Authorization", "Bearer "+token.Reveal())

	resp, err := r.Client.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: request %s: %w", r.slug(), err)
	}
	r.Client.observe(resp)
	return resp, nil
}

func (r *Repository) slug() string { return r.Owner + "/" + r.Name }

// escapePath escapes each segment while leaving the separators, so a ref such
// as refs/heads/main stays a path rather than becoming one escaped blob.
func escapePath(p string) string {
	segments := strings.Split(p, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
