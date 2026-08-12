package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// CommentMarker identifies a comment Dusk owns, so it can be edited in place
// rather than a new one being added on every push. A review thread with
// fourteen bot comments is one nobody reads.
const CommentMarker = "<!-- dusk:preview -->"

type comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// Comment posts or updates Dusk's comment on a pull request, found by its
// invisible marker because GitHub has no notion of "the one I made last time".
func (r *Repository) Comment(ctx context.Context, number int, body string) error {
	marked := CommentMarker + "\n" + body

	existing, err := r.findComment(ctx, number)
	if err != nil {
		return err
	}

	method, target := http.MethodPost, fmt.Sprintf("/issues/%d/comments", number)
	if existing != 0 {
		method, target = http.MethodPatch, fmt.Sprintf("/issues/comments/%d", existing)
	}

	payload, err := json.Marshal(map[string]string{"body": marked})
	if err != nil {
		return err
	}

	resp, err := r.send(ctx, method, target, payload)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("githubapp: comment on %s#%d: %w", r.slug(), number, statusError(resp))
	}
	return nil
}

// findComment returns the id of Dusk's existing comment, or zero.
func (r *Repository) findComment(ctx context.Context, number int) (int64, error) {
	target := fmt.Sprintf("/issues/%d/comments?per_page=100", number)
	resp, err := r.get(ctx, target, "application/vnd.github+json")
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("githubapp: read comments on %s#%d: %w", r.slug(), number, statusError(resp))
	}

	var comments []comment
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&comments); err != nil {
		return 0, fmt.Errorf("githubapp: decode comments on %s#%d: %w", r.slug(), number, err)
	}

	for _, existing := range comments {
		if strings.Contains(existing.Body, CommentMarker) {
			return existing.ID, nil
		}
	}
	return 0, nil
}
