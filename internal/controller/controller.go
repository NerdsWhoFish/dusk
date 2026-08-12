// Package controller keeps the catalog in step with GitHub.
//
// It enumerates the installations Dusk is permitted to read, reconciles each of
// their repositories, and sweeps periodically so the catalog cannot go quietly
// stale when a webhook is lost.
package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/FetchHQ/dusk/internal/index"
	"github.com/FetchHQ/dusk/internal/reconcile"
	"github.com/FetchHQ/dusk/internal/store"
	"github.com/FetchHQ/dusk/internal/write"
	"github.com/FetchHQ/dusk/pkg/githubapp"
)

// DefaultInterval is the poll floor. It is deliberately slow: webhooks carry
// the timely case, and this only has to catch what they lose.
const DefaultInterval = 10 * time.Minute

// Credentials supplies the App identity. It is re-read on every sweep so that
// completing onboarding does not require a restart.
type Credentials interface {
	Load() (*store.Credentials, error)
}

// Options are the controller's dependencies.
type Options struct {
	Index       *index.DB
	Client      *githubapp.Client
	Credentials Credentials

	// Accounts whose installations may be reconciled. Empty means the account
	// the App belongs to, and nothing else.
	Accounts []string

	Interval time.Duration
	Logger   *slog.Logger
	Now      func() time.Time
}

// Controller reconciles installations into the index.
type Controller struct {
	opts Options

	mu     sync.Mutex
	status map[index.Scope]Status

	// tokens is rebuilt when the App changes, so its cache survives between
	// sweeps rather than re-minting a token every ten minutes.
	tokens *githubapp.Tokens
	appID  int64

	// resolvedOwner caches an owner asked of GitHub, for credentials stored
	// before onboarding recorded one.
	resolvedOwner string

	// installations maps a repository to the installation that grants access.
	// A repository absent from it is one no sweep has seen, and therefore one
	// Dusk has no standing to write to.
	installations map[string]int64
}

// Target returns a writable handle on a repository a sweep has seen, so what
// Dusk can write to is bounded by what it was granted rather than by what an
// agent asks for.
func (c *Controller) Target(ctx context.Context, slug string) (write.Target, error) {
	c.mu.Lock()
	installationID, known := c.installations[slug]
	c.mu.Unlock()

	if !known {
		return nil, fmt.Errorf("controller: no installation grants access to %q. Dusk writes only to repositories it already reconciles", slug)
	}

	tokens, _, err := c.auth(ctx)
	if err != nil {
		return nil, err
	}

	owner, name, ok := strings.Cut(slug, "/")
	if !ok {
		return nil, fmt.Errorf("controller: %q is not an owner/name repository", slug)
	}
	install := &githubapp.Install{Client: c.opts.Client, Tokens: tokens, ID: installationID}
	return install.Repository(owner, name), nil
}

func (c *Controller) remember(slug string, installationID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.installations == nil {
		c.installations = map[string]int64{}
	}
	c.installations[slug] = installationID
}

// auth loads the App identity, a token cache for it, and the account it
// belongs to.
func (c *Controller) auth(ctx context.Context) (*githubapp.Tokens, string, error) {
	creds, err := c.opts.Credentials.Load()
	if err != nil {
		return nil, "", err
	}

	c.mu.Lock()
	if c.tokens == nil || c.appID != creds.AppID {
		c.appID = creds.AppID
		c.resolvedOwner = ""
		c.tokens = &githubapp.Tokens{
			Client: c.opts.Client,
			App:    githubapp.App{ID: creds.AppID, PrivateKey: creds.PrivateKey},
		}
	}
	tokens, cached := c.tokens, c.resolvedOwner
	c.mu.Unlock()

	if creds.Owner != "" {
		return tokens, creds.Owner, nil
	}
	if cached != "" {
		return tokens, cached, nil
	}

	// Onboarding before the owner was recorded leaves it empty, and an empty
	// owner allows nothing. Ask GitHub rather than refusing every installation.
	metadata, err := c.opts.Client.App(ctx, tokens.App)
	if err != nil {
		return nil, "", fmt.Errorf("controller: this App records no owner and GitHub could not be asked for one: %w", err)
	}

	c.mu.Lock()
	c.resolvedOwner = metadata.Owner.Login
	c.mu.Unlock()
	c.opts.Logger.Info("resolved the App owner from GitHub",
		"owner", metadata.Owner.Login, "reason", "onboarding predates it being recorded")
	return tokens, metadata.Owner.Login, nil
}

// Status is the outcome of the last reconcile of one repository, which is the
// surface that answers "did this sync, and why not".
type Status struct {
	Repository string
	GitRef     string
	Commit     string
	Entities   int
	Relations  int
	At         time.Time

	// Error is the last failure, kept alongside the previous good numbers
	// rather than replacing them, because the old graph is still what is served.
	Error string

	// Participating is false for a repository with no dusk.md.
	Participating bool
}

// New builds a Controller.
func New(opts Options) (*Controller, error) {
	if opts.Index == nil {
		return nil, errors.New("controller: an index is required")
	}
	if opts.Client == nil || opts.Credentials == nil {
		return nil, errors.New("controller: a client and a credential source are required")
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Controller{opts: opts, status: map[index.Scope]Status{}}, nil
}

// Permitted reports whether an account's installation may be reconciled. An
// App can be installed by anyone able to see it, so without this an uninvited
// installation writes content Dusk does not control into agents' context.
func (c *Controller) Permitted(account, owner string) bool {
	allowed := c.opts.Accounts
	if len(allowed) == 0 {
		allowed = []string{owner}
	}
	for _, candidate := range allowed {
		if candidate != "" && strings.EqualFold(candidate, account) {
			return true
		}
	}
	return false
}

// Sync reconciles every permitted installation's repositories and drops what it
// can no longer see. One repository failing neither stops the sweep nor removes
// what that repository already contributed.
func (c *Controller) Sync(ctx context.Context) error {
	tokens, owner, err := c.auth(ctx)
	if err != nil {
		c.opts.Logger.Info("sweep skipped: not onboarded yet", "reason", err)
		return nil
	}

	installations, err := c.opts.Client.Installations(ctx, tokens.App)
	if err != nil {
		return err
	}

	seen := map[index.Scope]bool{}
	complete := true

	for _, installation := range installations {
		account := installation.Account.Login
		if !c.Permitted(account, owner) {
			c.opts.Logger.Warn("installation ignored: account is not allowed",
				"account", account, "installation", installation.ID)
			continue
		}
		if !c.syncInstallation(ctx, tokens, installation, seen) {
			complete = false
		}
	}

	// Pruning only after a clean sweep is the same rule as ADR-0011: "I could
	// not look" must never be mistaken for "it is not there".
	if !complete {
		c.opts.Logger.Warn("skipping prune: this sweep was incomplete")
		return nil
	}
	return c.prune(ctx, seen)
}

// syncInstallation reports whether everything in the installation was read.
func (c *Controller) syncInstallation(ctx context.Context, tokens *githubapp.Tokens, installation githubapp.Installation, seen map[index.Scope]bool) bool {
	install := &githubapp.Install{Client: c.opts.Client, Tokens: tokens, ID: installation.ID}

	repositories, err := install.Repositories(ctx)
	if err != nil {
		c.opts.Logger.Error("could not list repositories",
			"account", installation.Account.Login, "installation", installation.ID, "error", err)
		return false
	}

	complete := true
	for _, repository := range repositories {
		gitRef := "refs/heads/" + repository.DefaultBranch
		seen[index.Scope{Repository: repository.Slug(), GitRef: gitRef}] = true
		c.remember(repository.Slug(), installation.ID)

		// Repositories disagree about what the default branch is called, so a
		// catalog-wide query needs to be told which ref each one contributes.
		if err := c.opts.Index.SetDefaultView(ctx, repository.Slug(), gitRef); err != nil {
			c.opts.Logger.Error("could not record the default view",
				"repository", repository.Slug(), "ref", gitRef, "error", err)
			complete = false
			continue
		}
		if err := c.reconcile(ctx, install, repository.Slug(), gitRef); err != nil {
			complete = false
		}
	}
	return complete
}

// SyncRepository reconciles one repository, which is what a webhook triggers.
func (c *Controller) SyncRepository(ctx context.Context, installationID int64, account, owner, name, gitRef string) error {
	tokens, appOwner, err := c.auth(ctx)
	if err != nil {
		c.opts.Logger.Info("delivery skipped: not onboarded yet", "reason", err)
		return nil
	}
	if !c.Permitted(account, appOwner) {
		c.opts.Logger.Warn("delivery ignored: account is not allowed",
			"account", account, "repository", owner+"/"+name)
		return nil
	}
	c.remember(owner+"/"+name, installationID)
	install := &githubapp.Install{Client: c.opts.Client, Tokens: tokens, ID: installationID}
	return c.reconcile(ctx, install, owner+"/"+name, gitRef)
}

func (c *Controller) reconcile(ctx context.Context, install *githubapp.Install, slug, gitRef string) error {
	owner, name, ok := strings.Cut(slug, "/")
	if !ok {
		return fmt.Errorf("controller: %q is not an owner/name repository", slug)
	}

	source := install.Repository(owner, name)
	graph, err := reconcile.New(source, c.opts.Index).Reconcile(ctx, slug, gitRef, c.opts.Now())
	if err != nil {
		c.record(index.Scope{Repository: slug, GitRef: gitRef}, func(s *Status) {
			s.At = c.opts.Now()
			s.Error = err.Error()
		})
		c.opts.Logger.Error("reconcile failed", "repository", slug, "ref", gitRef, "error", err)
		return err
	}

	c.record(index.Scope{Repository: slug, GitRef: gitRef}, func(s *Status) {
		*s = Status{
			Repository:    slug,
			GitRef:        gitRef,
			Commit:        graph.Commit,
			Entities:      len(graph.Entities),
			Relations:     len(graph.Relations),
			Participating: graph.Participating,
			At:            c.opts.Now(),
		}
	})
	c.opts.Logger.Info("reconciled",
		"repository", slug, "ref", gitRef, "commit", graph.Commit,
		"entities", len(graph.Entities), "relations", len(graph.Relations),
		"participating", graph.Participating)
	return nil
}

// prune removes contents for repositories the sweep did not see, which is how
// an uninstall or a revoked repository leaves the catalog.
func (c *Controller) prune(ctx context.Context, seen map[index.Scope]bool) error {
	scopes, err := c.opts.Index.Scopes(ctx)
	if err != nil {
		return err
	}

	for _, scope := range scopes {
		if seen[scope] {
			continue
		}
		if err := c.opts.Index.DropRepository(ctx, scope.Repository, scope.GitRef); err != nil {
			return err
		}
		c.forget(scope)
		c.opts.Logger.Info("dropped: no longer reachable",
			"repository", scope.Repository, "ref", scope.GitRef)
	}
	return nil
}

// Run drives Sync on an interval until ctx is done. This is ADR-0006's poll
// floor, and it runs whether or not webhooks are configured.
func (c *Controller) Run(ctx context.Context) {
	ticker := time.NewTicker(c.opts.Interval)
	defer ticker.Stop()

	for {
		if err := c.Sync(ctx); err != nil && ctx.Err() == nil {
			c.opts.Logger.Error("sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Status reports the last outcome per repository, ordered by repository.
func (c *Controller) Status() []Status {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]Status, 0, len(c.status))
	for _, status := range c.status {
		out = append(out, status)
	}
	return out
}

func (c *Controller) record(scope index.Scope, update func(*Status)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	status := c.status[scope]
	status.Repository, status.GitRef = scope.Repository, scope.GitRef
	update(&status)
	c.status[scope] = status
}

func (c *Controller) forget(scope index.Scope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.status, scope)
}
