package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/FetchHQ/dusk/internal/index"
	"github.com/FetchHQ/dusk/pkg/githubapp"
)

// PreviewRef is the ref a pull request's catalog is indexed under. It is not
// a real git ref: the head commit is, and this keys the preview so it cannot
// collide with a branch of the same name.
func PreviewRef(number int) string {
	return fmt.Sprintf("refs/pull/%d/head", number)
}

// Preview is one pull request to render the catalog at.
type Preview struct {
	InstallationID int64
	Account        string
	Owner          string
	Name           string

	// Number is the pull request. It keys the preview and names the comment.
	Number int

	// Head is the commit to read. Reading the ref would race a force push;
	// the commit is what the review is actually about.
	Head string

	// Closed means tear the preview down rather than build it.
	Closed bool
}

func (p Preview) slug() string { return p.Owner + "/" + p.Name }

// SyncPreview builds or removes a pull request's preview of the catalog. The
// index has been keyed by git ref since ADR-0008 so several versions can be
// live at once, and this is the feature that needed it.
func (c *Controller) SyncPreview(ctx context.Context, preview Preview) error {
	ref := PreviewRef(preview.Number)

	if preview.Closed {
		// A merged or abandoned pull request's preview is garbage, and one
		// delete scoped to its ref is the whole cleanup (ADR-0008).
		if err := c.opts.Index.DropGitRef(ctx, ref); err != nil {
			return err
		}
		c.forget(index.Scope{Repository: preview.slug(), GitRef: ref})
		c.opts.Logger.Info("preview removed", "repository", preview.slug(), "pull_request", preview.Number)
		return nil
	}

	tokens, appOwner, err := c.auth(ctx)
	if err != nil {
		c.opts.Logger.Info("preview skipped: not onboarded yet", "reason", err)
		return nil
	}
	if !c.Permitted(preview.Account, appOwner) {
		c.opts.Logger.Warn("preview ignored: account is not allowed",
			"account", preview.Account, "repository", preview.slug())
		return nil
	}

	install := &githubapp.Install{Client: c.opts.Client, Tokens: tokens, ID: preview.InstallationID}
	if err := c.reconcileWithRetry(ctx, install, preview.slug(), refOrCommit(ref, preview.Head)); err != nil {
		return err
	}

	c.opts.Logger.Info("preview built",
		"repository", preview.slug(), "pull_request", preview.Number, "head", short(preview.Head))

	return c.commentOnPreview(ctx, install, preview)
}

// commentOnPreview posts the semantic diff back to the pull request. A failure
// is logged and swallowed: the preview is already built, and a comment that
// would not post is not worth failing a delivery GitHub never redelivers.
func (c *Controller) commentOnPreview(ctx context.Context, install *githubapp.Install, preview Preview) error {
	base, err := c.defaultRefOf(ctx, install, preview)
	if err != nil {
		c.opts.Logger.Warn("could not find the base to compare against, skipping the comment",
			"repository", preview.slug(), "pull_request", preview.Number, "error", err)
		return nil
	}

	changes, err := c.Compare(ctx, base, preview.Number)
	if err != nil {
		return err
	}

	body := renderComment(changes, c.previewURL(preview))
	if err := install.Repository(preview.Owner, preview.Name).Comment(ctx, preview.Number, body); err != nil {
		c.opts.Logger.Warn("could not comment on the pull request",
			"repository", preview.slug(), "pull_request", preview.Number, "error", err)
	}
	return nil
}

func (c *Controller) defaultRefOf(ctx context.Context, install *githubapp.Install, preview Preview) (string, error) {
	branch, err := install.Repository(preview.Owner, preview.Name).DefaultBranch(ctx)
	if err != nil {
		return "", err
	}
	return "refs/heads/" + branch, nil
}

// previewURL links to the catalog as it would be after merge. Empty when the
// deployment has no host to link to, rather than a link that goes nowhere.
func (c *Controller) previewURL(preview Preview) string {
	if c.opts.PrivateHost == "" {
		return ""
	}
	return fmt.Sprintf("%s/?ref=%s", c.opts.PrivateHost, PreviewRef(preview.Number))
}

// Compare reports what merging a pull request would do to the catalog.
func (c *Controller) Compare(ctx context.Context, base string, number int) ([]index.Change, error) {
	return c.opts.Index.Diff(ctx, base, PreviewRef(number))
}

// refOrCommit prefers the commit, because a ref moves under a force push and
// the review is about a specific tree.
func refOrCommit(ref, head string) string {
	if strings.TrimSpace(head) == "" {
		return ref
	}
	return head
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
