package controller

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
)

// PreviewRef keeps repository-local PR numbers in separate catalog scopes.
func PreviewRef(repository string, number int) string {
	return index.PreviewRef(repository, number)
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
	ref := PreviewRef(preview.slug(), preview.Number)
	if _, _, ok := index.ParsePreviewRef(ref); !ok {
		return fmt.Errorf("controller: invalid pull request preview %q", ref)
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
	if preview.Closed {
		return c.closePreview(ctx, preview, ref)
	}

	install := &githubapp.Install{Client: c.opts.Client, Tokens: tokens, ID: preview.InstallationID}
	sourceRef := refOrCommit(fmt.Sprintf("refs/pull/%d/head", preview.Number), preview.Head)
	if err := c.reconcileWithRetryAt(ctx, install, preview.slug(), ref, sourceRef); err != nil {
		return err
	}

	c.opts.Logger.Info("preview built",
		"repository", preview.slug(), "pull_request", preview.Number, "head", short(preview.Head))

	return c.commentOnPreview(ctx, install, preview)
}

func (c *Controller) closePreview(ctx context.Context, preview Preview, ref string) error {
	scope := index.Scope{Repository: preview.slug(), GitRef: ref}
	leave, err := c.enterReconcile(ctx, scope)
	if err != nil {
		return err
	}
	defer leave()
	if err := c.opts.Index.DropRepository(ctx, scope.Repository, scope.GitRef); err != nil {
		return err
	}
	c.evict(scope)
	c.opts.Logger.Info("preview removed", "repository", preview.slug(), "pull_request", preview.Number)
	return nil
}

// commentOnPreview posts the semantic diff back to the pull request. A failure
// is logged and swallowed: the preview is already built, and a comment that
// would not post is not worth failing a delivery GitHub never redelivers.
func (c *Controller) commentOnPreview(ctx context.Context, install *githubapp.Install, preview Preview) error {
	changes, err := c.Compare(ctx, preview.slug(), preview.Number)
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

// previewURL links to the catalog as it would be after merge. Empty when the
// deployment has no host to link to, rather than a link that goes nowhere.
func (c *Controller) previewURL(preview Preview) string {
	if c.opts.PrivateHost == "" {
		return ""
	}
	return c.opts.PrivateHost + "/?" + url.Values{"ref": {PreviewRef(preview.slug(), preview.Number)}}.Encode()
}

// Compare reports what merging a pull request would do to the catalog.
func (c *Controller) Compare(ctx context.Context, repository string, number int) ([]index.Change, error) {
	ref, err := c.opts.Index.ResolvePreview(ctx, PreviewRef(repository, number))
	if err != nil {
		return nil, err
	}
	return c.opts.Index.Diff(ctx, "", ref)
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
