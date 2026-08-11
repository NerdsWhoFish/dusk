# 27. The design target is a single operator and their agents

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk has been designed without ever stating who it is for, and an unstated answer has been accumulating in its place.

[ADR-0003](0003-license.md) says Dusk "is intended to be adopted by platform teams inside companies".
[ADR-0005](0005-github-app-and-access-modes.md) calls proposal mode "the setting most teams will actually live in".
[ADR-0013](0013-layout-and-pages.md) argues that "a service's own team knows best how to present it".

None of that was argued.
It was inherited, and the mechanism is worth naming: a product that defines itself against Backstage absorbs Backstage's audience by default, because every comparison is drawn on that audience's terms.

This matters because the question does not stay theoretical.
Whether there is a permission model beyond repo access, whether zero configuration has to work, whether ownership is a field or a feature, and what an example in the documentation looks like are all answered differently depending on who is at the other end.
Left unstated, each of those gets re-argued from scratch, and the answers drift apart.

## Considered Options

1. **Platform teams inside companies**, the audience Backstage serves.
2. **A single operator and their agents**: one person with more systems than memory, and the agents that work on them.
3. **Refuse to choose**, and serve both equally.

## Decision Outcome

Chosen: **option 2**.

Dusk is designed for one person running their own infrastructure, with agents doing much of the work.
Homelabs, personal clusters, side projects, and the accumulated sprawl of somebody who has been self-hosting for a decade.

Team use is supported and is not designed against.
What changes is the tie-breaker: **when a decision could go either way, it resolves for the single operator**, and a team-shaped answer never wins on the grounds that a team would prefer it.

The thesis is the reason.
Dusk exists because curation burden falls on a human with better things to do, and that argument is *weakest* against a platform team, which has staff whose actual job is to curate.
It is strongest where there is exactly one person, no chance of a documentation rota, and no colleague who will notice the catalog has gone stale.
The audience where the product is most needed is the one where it was least aimed.

### What this settles

**There is no permission model beyond repository access.**
[ADR-0012](0012-viewing-auth.md) already derives viewing authorization from repo access to avoid a second model that could drift.
For a single operator that stops being a simplification worth defending and becomes simply correct.

**Zero configuration is a working configuration.**
There is no platform team to run a rollout, so anything that requires setup before Dusk is useful is a defect rather than an onboarding step.

**Ownership is a field, never a feature.**
[DESIGN.md](../DESIGN.md) already lists "not an org chart tool" as a non-goal, and this is why.
Where there is one operator, an owner field is a label, and building hierarchy on top of it would be building for a user who does not exist.

**Agents are the review capacity, not a supplement to it.**
A single operator has no colleague to review a catalog change, so PR previews, semantic diffs, and the comment bot are not conveniences layered on a human review process, they *are* the review process.
This raises their priority rather than lowering it.

**Documentation examples are homelab-shaped.**
An example is the fastest signal about who a tool expects, and `platform/checkout` tells a reader they are in the wrong place.

## Consequences

### Good

- The tie-breaker exists, so ambiguous decisions stop being re-argued from first principles and stop drifting apart.
- The product thesis and the audience finally agree. Dusk is aimed at the case where self-maintenance is the only thing that could possibly work, rather than the case where a team could paper over its absence with labour.
- Several decisions already made get retroactively coherent rather than merely defensible: no second permission model, a single binary, no org chart, strong zero-config defaults.
- It is an underserved audience. Backstage is unusable at this scale, and the alternative most homelabbers actually run is a README table that went stale a year ago.

### Bad

- [ADR-0003](0003-license.md)'s stated rationale is now off-target. The licensing *decision* stands unchanged, so this ADR does not supersede it, but anyone reading its context statement will find an audience Dusk no longer aims at.
- OIDC and SAML were deferred by [ADR-0012](0012-viewing-auth.md) rather than rejected. This moves them closer to never, which is a harder conversation if a company does adopt Dusk later.
- "For homelabbers" undersells the engineering and may repel exactly the readers most able to fund or contribute to it. Positioning is not the same as design target, and conflating the two would be a mistake this ADR does not license.
- The most expensive risk is that the target leaks into the data model. Assuming one identity in the schema, the storage layer, or the write path would be cheap now and very costly to undo. **The target is a tie-breaker for product decisions, not permission to hard-code single-tenancy.**
- Team-shaped contributions become harder to evaluate, because "a team would want this" is no longer an argument on its own.

### Rejected because

- **Option 1** was rejected because it is Backstage's audience, Backstage serves it with vastly more resources, and Dusk's central argument is weakest there. Competing on someone else's ground with a thesis that does not apply to it is the worst of both.
- **Option 3** was rejected because refusing to choose is what produced this ADR. An unstated target does not stay neutral, it gets filled in silently and inconsistently, which is the same failure [ADR-0018](0018-normalization-at-the-edge.md) named when it refused to leave a boundary unclear.
