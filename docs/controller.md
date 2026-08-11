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

**The poll floor** sweeps everything on an interval regardless, default ten minutes.
It looks redundant next to webhooks and is not: deliveries are lost in normal operation, and a catalog with no poll underneath goes stale with no signal, which for this product is the worst available bug ([ADR-0006](../adr/0006-reconcile-triggering.md)).

Poll-only is a fully supported configuration, not a degraded one. Nothing requires a public endpoint.

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
