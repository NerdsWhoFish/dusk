# 6. Webhook-triggered reconcile, with a periodic poll floor

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk must notice when a watched repo changes and reconcile at the new ref.

Polling is the simplest mechanism and works everywhere, including behind NAT and on a laptop.
Webhooks are faster and cheaper at scale, and they arrive free with the GitHub App established in [ADR-0005](0005-github-app-and-access-modes.md), but they require a publicly reachable endpoint.

A common argument for going webhook-only is that polling consumes API quota.
That argument is weaker than it appears.
Detecting whether a repo changed is a `git ls-remote` against the git transport, not a call to `api.github.com`, so it does not consume the REST rate limit at all.
Where API quota actually burns is ingesters pulling rich data such as issues, pull request state, and workflow runs, which is a separate concern from reconcile triggering.

The decisive consideration is a failure mode.
Webhook deliveries are lost in normal operation: the endpoint is down during a deploy, a tunnel flaps, the provider has an incident, a secret is rotated.
A system with no poll underneath goes **silently stale** when that happens, and nothing signals it.
For a product whose entire value is that the catalog is correct, silent staleness is the worst available bug.

## Considered Options

1. **Poll only.**
2. **Webhook only.**
3. **Webhook for latency, with a periodic poll as a floor.**

## Decision Outcome

Chosen: **option 3**.

- When a webhook endpoint is configured, deliveries trigger immediate reconcile.
- A periodic poll runs regardless, on a slow interval (15 to 30 minutes by default), using `git ls-remote` to compare refs.
- With no webhook configured, poll-only is a fully supported configuration, not a degraded one.

The webhook endpoint validates HMAC signatures using the secret GitHub generated during the manifest flow, and rejects replayed deliveries.

The poll floor is load-bearing and must not be removed later as redundant.
This ADR exists largely to record why.

### Consequences

#### Good

- Dusk works out of the box with no public endpoint, no tunnel, and no inbound firewall rule. Adding a webhook is an optimization, not a prerequisite.
- A dropped delivery costs latency, not correctness. The catalog self-heals within one poll interval.
- The poll is cheap. `ls-remote` does not touch the REST quota, so watching many repos does not compete with ingesters for API budget.
- This is the same design Flux and ArgoCD converged on, which means the operational behaviour will be familiar to the target user.

#### Bad

- Two trigger paths must converge on one reconcile implementation, and both must be idempotent. A webhook and a poll firing near-simultaneously must not produce duplicate work or conflicting writes.
- The poll interval is a tuning knob that will be wrong for somebody, and exposing it invites the usual arguments about defaults.
- Constant background polling produces log noise and a steady trickle of network traffic even when nothing changes, which needs sensible quieting so real events stay visible.
- Webhook handling brings its own surface: signature validation, replay protection, and an endpoint that is public by definition.

#### Rejected because

- Poll only was rejected on latency. Catalog updates arriving up to half an hour late undercuts the premise that an agent's work is reflected immediately.
- Webhook only was rejected on the silent staleness failure mode above. The API-quota argument that motivates it does not survive the observation that `ls-remote` is not a REST call, and no reliability argument replaces it.
