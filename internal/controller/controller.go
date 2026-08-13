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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/reconcile"
	"github.com/NerdsWhoFish/dusk/internal/store"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
)

// DefaultInterval is the poll floor, deliberately slow. Webhooks carry the
// timely case and a transient failure is retried rather than waited out, so
// what is left is a delivery that never arrived, and a day catches that.
const DefaultInterval = 24 * time.Hour

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

	// PrivateHost is where a person reaches this Dusk, used to link a pull
	// request comment at the preview. Empty means no link rather than a
	// broken one.
	PrivateHost string

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

	// reconciled is the last commit finished for each scope. A commit reaches
	// it only on success, so an unfinished one is retried by the next sweep.
	reconciled map[index.Scope]string
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

	c.reportBudget()

	// Pruning only after a clean sweep is the same rule as ADR-0011: "I could
	// not look" must never be mistaken for "it is not there".
	if !complete {
		c.opts.Logger.Warn("skipping prune: this sweep was incomplete")
		return nil
	}
	return c.prune(ctx, seen)
}

// reportBudget logs what the sweep left behind. Running out of requests makes
// the catalog wrong rather than slow, so the approach has to be visible before
// the wall is hit rather than diagnosed from a pile of 403s afterwards.
func (c *Controller) reportBudget() {
	rate := c.opts.Client.RateLimit()
	switch {
	case !rate.Known:
		return
	case rate.Low():
		c.opts.Logger.Warn("github api budget is running low",
			"remaining", rate.Remaining, "limit", rate.Limit, "resets", rate.Reset)
	default:
		c.opts.Logger.Debug("github api budget",
			"remaining", rate.Remaining, "limit", rate.Limit)
	}
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
	return c.SyncPush(ctx, Push{
		InstallationID: installationID,
		Account:        account,
		Owner:          owner,
		Name:           name,
		GitRef:         gitRef,
	})
}

// Push is one delivery's worth of work.
type Push struct {
	InstallationID int64
	Account        string
	Owner          string
	Name           string
	GitRef         string

	// Files is what the push touched. A nil slice means the delivery could not
	// be trusted to list them, and the repository is read as if it had not said.
	Files []string
}

func (p Push) slug() string { return p.Owner + "/" + p.Name }

// SyncPush reconciles one repository from a delivery, skipping the read
// entirely when the files the push touched cannot have changed the catalog.
func (c *Controller) SyncPush(ctx context.Context, push Push) error {
	tokens, appOwner, err := c.auth(ctx)
	if err != nil {
		c.opts.Logger.Info("delivery skipped: not onboarded yet", "reason", err)
		return nil
	}
	if !c.Permitted(push.Account, appOwner) {
		c.opts.Logger.Warn("delivery ignored: account is not allowed",
			"account", push.Account, "repository", push.slug())
		return nil
	}
	if c.irrelevant(ctx, push) {
		return nil
	}

	c.remember(push.slug(), push.InstallationID)
	install := &githubapp.Install{Client: c.opts.Client, Tokens: tokens, ID: push.InstallationID}
	return c.reconcileWithRetry(ctx, install, push.slug(), push.GitRef)
}

// irrelevant reports whether a push provably cannot have changed the catalog,
// making it answerable for zero requests. A skip is never recorded as
// reconciled, so a sweep still corrects this if the judgement was wrong.
func (c *Controller) irrelevant(ctx context.Context, push Push) bool {
	if push.Files == nil {
		return false
	}

	participates, err := c.opts.Index.Participates(ctx, push.slug(), push.GitRef)
	if err != nil {
		c.opts.Logger.Warn("could not tell whether the repository participates, reading it",
			"repository", push.slug(), "error", err)
		return false
	}

	if participates {
		if slices.ContainsFunc(push.Files, catalogfs.IsCatalogFile) {
			return false
		}
		c.opts.Logger.Debug("delivery skipped: the push touched no markdown",
			"repository", push.slug(), "files", len(push.Files))
		return true
	}

	if slices.Contains(push.Files, reconcile.RootFile) {
		return false
	}
	c.opts.Logger.Debug("delivery skipped: the repository has no dusk.md and the push did not add one",
		"repository", push.slug(), "files", len(push.Files))
	return true
}

// deliveryAttempts bounds a webhook-triggered reconcile. GitHub is answered
// before the work runs and never redelivers, so without this one transient
// failure leaves the catalog wrong until the poll floor comes round.
const deliveryAttempts = 3

// deliveryBackoff is the wait before each retry. Short, because the delivery
// context has a timeout and a reconcile that needs minutes is not transient.
var deliveryBackoff = []time.Duration{2 * time.Second, 8 * time.Second}

// reconcileWithRetry is the delivery path. A sweep does not use it, because a
// sweep retries by running again.
func (c *Controller) reconcileWithRetry(ctx context.Context, install *githubapp.Install, slug, gitRef string) error {
	var err error
	for attempt := range deliveryAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(deliveryBackoff[attempt-1]):
			}
			c.opts.Logger.Info("retrying a failed delivery",
				"repository", slug, "ref", gitRef, "attempt", attempt+1)
		}

		err = c.reconcile(ctx, install, slug, gitRef)
		if err == nil || !retryable(err) {
			return err
		}
	}
	c.opts.Logger.Error("giving up on a delivery; the next sweep will retry",
		"repository", slug, "ref", gitRef, "error", err)
	return err
}

// retryable reports whether trying again could plausibly succeed. A file that
// does not parse will not parse on the second attempt, and retrying it only
// delays the error reaching whoever wrote it.
// retryable reports whether trying again soon could plausibly work. Both cases
// it excludes fail identically however many times they are attempted, and the
// budget one would spend requests it does not have to make things worse.
func retryable(err error) bool {
	var parse duskmd.Errors
	return !errors.As(err, &parse) && !errors.Is(err, githubapp.ErrRateLimited)
}

func (c *Controller) reconcile(ctx context.Context, install *githubapp.Install, slug, gitRef string) error {
	owner, name, ok := strings.Cut(slug, "/")
	if !ok {
		return fmt.Errorf("controller: %q is not an owner/name repository", slug)
	}
	scope := index.Scope{Repository: slug, GitRef: gitRef}

	source := &reconcile.Tarball{Repo: install.Repository(owner, name)}
	commit, err := source.Resolve(ctx, gitRef)
	if err != nil {
		return c.failed(scope, err)
	}

	// Nothing moved, so there is nothing to read. This is the common case and
	// the whole reason a sweep of an idle installation is affordable.
	if c.done(scope, commit) {
		return nil
	}

	// A repository with no dusk.md has not opted in, so it is never downloaded.
	// Recording the commit stops it being probed again until it changes.
	if _, err := source.Tree(ctx, commit); err != nil {
		if errors.Is(err, reconcile.ErrNotParticipating) {
			if err := c.opts.Index.DropRepository(ctx, slug, gitRef); err != nil {
				return c.failed(scope, err)
			}
			c.finish(scope, commit)
			c.forget(scope)
			return nil
		}
		return c.failed(scope, err)
	}

	graph, err := reconcile.New(source, c.opts.Index).Reconcile(ctx, slug, gitRef, c.opts.Now())
	if err != nil {
		return c.failed(scope, err)
	}
	c.finish(scope, commit)

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
		// An ingester's scope occupies the repository slot but is not one, so
		// a sweep never sees it and would drop everything observed on every
		// pass. Only the ingester that owns it may replace it (ADR-0034).
		if index.IsObserved(scope.Repository) {
			continue
		}
		// A pull request preview is keyed by ref and torn down when the pull
		// request closes, not by a sweep that has never heard of it.
		if strings.HasPrefix(scope.GitRef, "refs/pull/") {
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

// done reports that this commit has already been reconciled successfully.
func (c *Controller) done(scope index.Scope, commit string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reconciled[scope] == commit
}

// finish records a commit as handled. Only success records, which is what makes
// a failure retry itself: the next sweep sees a commit it has not finished.
func (c *Controller) finish(scope index.Scope, commit string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reconciled == nil {
		c.reconciled = map[index.Scope]string{}
	}
	c.reconciled[scope] = commit
}

// failed records the error against the repository and returns it unchanged. The
// commit is deliberately not recorded, so the work is retried.
func (c *Controller) failed(scope index.Scope, err error) error {
	c.record(scope, func(s *Status) {
		s.At = c.opts.Now()
		s.Error = err.Error()
	})
	c.opts.Logger.Error("reconcile failed",
		"repository", scope.Repository, "ref", scope.GitRef, "error", err)
	return err
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
