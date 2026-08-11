package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
)

// Repository reads one repository's catalog files as an installed App. It reads
// over the API rather than cloning, because a reconcile wants one file plus
// whatever it points at, and a clone fetches all of history to deliver them.
type Repository struct {
	Client         *Client
	Tokens         *Tokens
	InstallationID int64
	Owner          string
	Name           string

	mu    sync.Mutex
	trees map[string][]string
}

// maxFileSize bounds a catalog file. The contents API will serve far more, but
// a dusk.md this large is a mistake worth failing on rather than ingesting.
const maxFileSize = 1 << 20

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

// ReadFile returns the contents of filePath at commit.
func (r *Repository) ReadFile(ctx context.Context, commit, filePath string) ([]byte, error) {
	target := "/contents/" + escapePath(filePath) + "?ref=" + url.QueryEscape(commit)
	resp, err := r.get(ctx, target, "application/vnd.github.raw")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("githubapp: %s %q at %s: %w", r.slug(), filePath, short(commit), fs.ErrNotExist)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: read %q from %s: %w", filePath, r.slug(), statusError(resp))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("githubapp: read %q from %s: %w", filePath, r.slug(), err)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("githubapp: %q in %s is larger than %d bytes", filePath, r.slug(), maxFileSize)
	}
	return data, nil
}

// Glob returns the paths at commit matching pattern, in lexical order.
func (r *Repository) Glob(ctx context.Context, commit, pattern string) ([]string, error) {
	paths, err := r.tree(ctx, commit)
	if err != nil {
		return nil, err
	}

	var matches []string
	for _, candidate := range paths {
		ok, err := path.Match(pattern, candidate)
		if err != nil {
			return nil, fmt.Errorf("githubapp: include pattern %q is malformed: %w", pattern, err)
		}
		if ok {
			matches = append(matches, candidate)
		}
	}
	slices.Sort(matches)
	return matches, nil
}

// tree lists every file path at commit, once. Caching is safe without any
// invalidation because a commit's tree cannot change.
func (r *Repository) tree(ctx context.Context, commit string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cached, ok := r.trees[commit]; ok {
		return cached, nil
	}

	paths, err := r.fetchTree(ctx, commit)
	if err != nil {
		return nil, err
	}
	if r.trees == nil {
		r.trees = make(map[string][]string)
	}
	r.trees[commit] = paths
	return paths, nil
}

func (r *Repository) fetchTree(ctx context.Context, commit string) ([]string, error) {
	resp, err := r.get(ctx, "/git/trees/"+escapePath(commit)+"?recursive=1", "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: list %s at %s: %w", r.slug(), short(commit), statusError(resp))
	}

	var tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, fmt.Errorf("githubapp: decode tree for %s: %w", r.slug(), err)
	}

	// A truncated listing would silently drop catalog files, producing a
	// catalog that is confidently incomplete rather than one that failed.
	if tree.Truncated {
		return nil, fmt.Errorf("githubapp: %s is too large for GitHub to list in one response, so an include pattern cannot be resolved safely", r.slug())
	}

	paths := make([]string, 0, len(tree.Tree))
	for _, entry := range tree.Tree {
		if entry.Type == "blob" {
			paths = append(paths, entry.Path)
		}
	}
	return paths, nil
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
