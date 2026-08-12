package githubapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
)

// FileContents is a file and the blob sha that identifies this exact version.
type FileContents struct {
	Data []byte

	// SHA identifies the blob being replaced. An update presents it so GitHub
	// refuses a write that raced another, which is the same read-before-write
	// guarantee proof tokens give at the catalog layer, enforced again at git.
	SHA string
}

// ReadFileContents returns a file with the blob sha needed to update it. The
// reconciler's ReadFile is the cheaper raw read; this one pays for the JSON
// envelope and is used only on the write path.
func (r *Repository) ReadFileContents(ctx context.Context, ref, filePath string) (*FileContents, error) {
	target := "/contents/" + escapePath(filePath) + "?ref=" + url.QueryEscape(ref)
	resp, err := r.get(ctx, target, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("githubapp: %s %q: %w", r.slug(), filePath, fs.ErrNotExist)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: read %q from %s: %w", filePath, r.slug(), statusError(resp))
	}

	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		SHA      string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("githubapp: decode %q: %w", filePath, err)
	}
	if payload.Encoding != "base64" {
		return nil, fmt.Errorf("githubapp: %q came back %s encoded, want base64", filePath, payload.Encoding)
	}

	// GitHub wraps the base64 at 60 columns, which the strict decoder rejects.
	data, err := base64.StdEncoding.DecodeString(stripNewlines(payload.Content))
	if err != nil {
		return nil, fmt.Errorf("githubapp: decode %q: %w", filePath, err)
	}
	return &FileContents{Data: data, SHA: payload.SHA}, nil
}

// FileCommit is one file written as one commit.
type FileCommit struct {
	Branch  string
	Path    string
	Message string
	Content []byte

	// ReplacingSHA is the blob sha being overwritten, empty when creating. A
	// wrong one is rejected by GitHub rather than silently overwriting.
	ReplacingSHA string
}

// Commit is where a write landed.
type Commit struct {
	SHA string
	URL string
}

// CommitFile writes one file as one commit, returning where it landed so an
// agent can hand a human a link rather than asserting it worked (ADR-0010).
func (r *Repository) CommitFile(ctx context.Context, write FileCommit) (*Commit, error) {
	if write.Branch == "" || write.Path == "" || write.Message == "" {
		return nil, fmt.Errorf("githubapp: a commit needs a branch, a path, and a message")
	}

	body := map[string]any{
		"message": write.Message,
		"content": base64.StdEncoding.EncodeToString(write.Content),
		"branch":  write.Branch,
	}
	if write.ReplacingSHA != "" {
		body["sha"] = write.ReplacingSHA
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("githubapp: encode commit: %w", err)
	}

	resp, err := r.send(ctx, http.MethodPut, "/contents/"+escapePath(write.Path), encoded)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// 409 is GitHub reporting that the blob moved since it was read, which is a
	// collision rather than a malformed request and reads very differently.
	if resp.StatusCode == http.StatusConflict {
		return nil, fmt.Errorf("githubapp: %q in %s changed since it was read: %w", write.Path, r.slug(), statusError(resp))
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("githubapp: commit %q to %s: %w", write.Path, r.slug(), statusError(resp))
	}

	var result struct {
		Commit struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("githubapp: decode commit result: %w", err)
	}
	return &Commit{SHA: result.Commit.SHA, URL: result.Commit.HTMLURL}, nil
}

// DefaultBranch reports the branch a write mode commit lands on.
func (r *Repository) DefaultBranch(ctx context.Context) (string, error) {
	resp, err := r.get(ctx, "", "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("githubapp: read %s: %w", r.slug(), statusError(resp))
	}

	var payload struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("githubapp: decode %s: %w", r.slug(), err)
	}
	if payload.DefaultBranch == "" {
		return "", fmt.Errorf("githubapp: %s reports no default branch", r.slug())
	}
	return payload.DefaultBranch, nil
}

func (r *Repository) send(ctx context.Context, method, target string, body []byte) (*http.Response, error) {
	token, err := r.Tokens.Token(ctx, r.InstallationID)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s%s", r.Client.baseURL(), escapePath(r.Owner), escapePath(r.Name), target)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("githubapp: build request for %s: %w", r.slug(), err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	req.Header.Set("Authorization", "Bearer "+token.Reveal())

	resp, err := r.Client.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: request %s: %w", r.slug(), err)
	}
	r.Client.observe(resp)
	return resp, nil
}

func stripNewlines(s string) string {
	var out bytes.Buffer
	for _, r := range s {
		if r != '\n' && r != '\r' {
			out.WriteRune(r)
		}
	}
	return out.String()
}
