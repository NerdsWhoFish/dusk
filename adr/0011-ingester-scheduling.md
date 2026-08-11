# 11. Ingester scheduling, shared API budget, and never deleting on failure

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Ingesters are the plugins that observe external systems and emit entities, per [ADR-0002](0002-plugin-protocol.md) and [ADR-0007](0007-entity-schema.md).
Unlike reconcile, which is cheap because it uses `git ls-remote`, ingesters pull rich data such as issues, pull request state, workflow runs, and cluster objects.
That is where API quota actually burns, as noted in [ADR-0006](0006-reconcile-triggering.md).

Three problems need answers before a second ingester ships.

Ingesters compete for the same quota, so scheduling them independently means the noisiest one starves the rest.
A broken ingester that retries aggressively can drain the budget for everything else.
And an ingester that fails must not be mistaken for an ingester that observed nothing.

That last one is the dangerous case.
If a failed run is treated as an empty result, entities disappear from the catalog, and a transient timeout looks identical to a decommissioned service.

## Considered Options

For scheduling:

1. Each ingester runs on its own interval, independently.
2. Central scheduler with a shared budget and concurrency cap.

For failure:

1. A failed run produces no observations, so prior observations expire.
2. A failed run preserves prior observations and marks them stale.

## Decision Outcome

### Scheduling

Each ingester declares `interval` and `timeout` in its markdown frontmatter.
A central scheduler owns execution, subject to two global limits:

- **A shared API budget per source system.** Every GitHub ingester draws from one pool rather than each assuming it has the full quota.
- **A concurrency cap**, so ingesters do not stampede on startup or after a restart.

Repeated failure triggers exponential backoff, and sustained failure trips a circuit breaker so that one broken ingester cannot consume the budget the others need.

### Failure never deletes

**A failing ingester never removes entities.**

Prior observations are preserved and marked stale.
Staleness is computed from the `observed_at` field that [ADR-0007](0007-entity-schema.md) puts on every message, so no separate freshness tracking is required.

Failures surface in the sync observability feed alongside reconcile status.

Deletion only happens when an ingester **succeeds** and reports that a previously observed entity is genuinely absent.

## Consequences

### Good

- A shared budget makes quota exhaustion a scheduling problem with a visible cause, rather than an intermittent mystery that shows up as random ingester failures.
- The circuit breaker contains blast radius. One misconfigured ingester degrades itself rather than the whole catalog.
- Preserving observations on failure means a network blip cannot be mistaken for a decommissioned service. This is the single most important rule here, because silent deletion would destroy trust in the catalog permanently and would be very easy to implement by accident.
- Staleness computed from `observed_at` means the UI can always show how old an observation is, and a stale entity is visibly stale rather than quietly wrong.

### Bad

- A central scheduler is more machinery than independent timers, and it becomes a component that can itself fail.
- A shared budget requires deciding how to divide it when ingesters compete, and any fairness policy will be wrong for somebody.
- Never deleting on failure means genuinely removed entities linger until a successful run confirms their absence. A permanently broken ingester leaves stale entities in place indefinitely, which is the accepted cost of not deleting things wrongly.
- Backoff makes recovery slower after a transient failure, which is the usual tradeoff and is worth it.

### Rejected because

- Independent intervals were rejected because ingesters share a quota whether or not the design acknowledges it. Pretending otherwise moves the problem to production.
- Expiring observations on failure was rejected outright. It conflates "I could not look" with "it is not there", and those two must never be the same thing in a catalog people rely on.
