# Controller

The controller keeps the catalog in step with GitHub.

It discovers what to read, reconciles it, sweeps periodically so nothing goes quietly stale, and reports what happened.
Implemented in `internal/controller`.

## What it reads is discovered, not configured

There is no list of repositories to maintain.
The controller lists the App's installations, lists each installation's repositories, and reconciles every one at its default branch.

That is what makes [ADR-0004](../adr/0004-dusk-md-convention.md)'s cold start work: install the App on twenty repositories that already contain a `dusk.md` and the catalog fills itself.

## Only allowlisted accounts

An installation is reconciled **only if its account is allowed**, defaulting to the account the App itself belongs to, and widened with `DUSK_ALLOWED_ACCOUNTS`.

This is a security control, not tidiness.
Anyone able to see an App can install it, and an organisation admin can install an organisation App on any repository in that organisation.
Catalog content is injected into agent context ([ADR-0014](../adr/0014-agent-context-injection.md)), so a repository Dusk never meant to read becomes text an agent treats as fact.

Both paths check it: a sweep skips a disallowed installation, and a webhook delivery for one is refused.
Refusals are logged rather than passed over, because an operator who wanted that installation needs to know why nothing appeared, and one who did not needs to know it happened.

Full reasoning in [ADR-0030](../adr/0030-account-allowlist.md).

## Two triggers, one of which is the floor

**Webhooks** carry the timely case.
A `push` reconciles that one repository at that one ref; an `installation` or `installation_repositories` event triggers a full sweep, since what Dusk may read has changed.

Deliveries are answered immediately and the work runs behind the response, so GitHub is never waiting on a reconcile.

That speed has a cost: GitHub is told the delivery succeeded before the work is attempted, so it never redelivers.
A delivery therefore retries its own work, three attempts backing off from two seconds, and gives up on anything that would fail the same way every time.
A file that does not parse will not parse on retry, and a spent API budget cannot be spent harder.

**The poll floor** sweeps everything on an interval regardless, default twenty-four hours.
It looks redundant next to webhooks and is not: deliveries are lost in normal operation, and a catalog with no poll underneath goes stale with no signal, which for this product is the worst available bug ([ADR-0006](../adr/0006-reconcile-triggering.md)).

The floor is slow because it can afford to be.
A repository whose commit has not moved is recognised for one request and not read, and a repository with no `dusk.md` is never downloaded ([ADR-0032](../adr/0032-tarball-reads.md)), so a sweep of a mostly idle installation costs about one request per repository.
Only a failure records nothing, which is what makes the next sweep retry it.

Poll-only is a fully supported configuration, not a degraded one. Nothing requires a public endpoint.

## The API budget is watched

A GitHub App installation gets on the order of five thousand requests an hour, shared across every repository it can see.
Exhausting it does not slow the catalog down, it makes the catalog wrong, because every request after the limit fails until the hour rolls over.

Every response carries the remaining budget, so Dusk reads it from responses it was making anyway rather than spending a request to ask.
Each sweep logs what it left behind, and warns once the remaining budget falls below a third.

A refusal for budget reasons is distinguished from a refusal for permission reasons, which matters because GitHub returns 403 for both.
`errors.Is(err, githubapp.ErrRateLimited)` separates them, and a `*githubapp.RateLimitError` carries when the budget returns.
Nothing retries into a rate limit.

### A push that cannot have changed the catalog costs nothing

A push payload lists the files each of its commits touched, and that list answers two questions without asking GitHub anything.

A repository that declares nothing only becomes interesting when a root `dusk.md` appears in the push.
A repository that does declare something cannot change without some markdown changing.
Either way the answer comes from the index, which is SQLite, so the common case of a code push is free.

The list is trusted only when it is complete.
GitHub caps a payload at twenty commits and does not flag the truncation, so a payload at the cap is treated as unknown, as are a created branch, a force push, and a delivery carrying no commits.
Unknown means read the repository, never skip it.

**A skip is not recorded as reconciled.**
The commit stays unrecorded, so if this judgement was ever wrong the next sweep still corrects it.
Recording it would turn a mistake here into permanent silent staleness, which is the one failure this product cannot have.

## Failure never looks like deletion

A sweep removes contents belonging to repositories it can no longer see, which is how an uninstall leaves the catalog.

That pruning happens **only after a complete sweep**.
If listing an installation failed, the sweep is incomplete and nothing is pruned, because "I could not look" must never be mistaken for "it is not there" ([ADR-0011](../adr/0011-ingester-scheduling.md)).

One repository failing does not stop the others, and does not remove what that repository already contributed.
The previous graph stays served while the error is reported.

## Status

Every reconcile records its outcome per repository: the commit, how many entities and relations landed, when, and the last error if there was one.

An error is kept **alongside** the previous good numbers rather than replacing them, because the old graph is still what queries return.

## Credentials are re-read, not captured

The controller loads the App identity on every sweep rather than at startup.

Onboarding therefore takes effect without a restart: a Dusk that starts un-onboarded logs that it is skipping, and begins reconciling once setup completes.

An install onboarded before the App's owner was recorded has no owner stored, and an empty owner allows nothing.
Rather than silently reconciling zero repositories, Dusk asks GitHub for the App's owner and carries on, logging that it did.

Signing in with GitHub follows the same rule for the same reason.
The App registered at `/setup` is itself an OAuth provider, its manifest already claims `/auth/callback`, and the exchange returns a client id and secret that are stored with the rest.
So there is nothing to configure: the "Sign in with GitHub" button appears once an App is registered, on the pod that registered it, without a restart.

`DUSK_GITHUB_CLIENT_ID` and `DUSK_GITHUB_CLIENT_SECRET` override it, and exist only to point a deployment at an app it did not register itself.
