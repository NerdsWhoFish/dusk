# 30. Only allowlisted accounts are reconciled

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk discovers what to read by listing its own installations, which is what makes [ADR-0004](0004-dusk-md-convention.md)'s cold start work: install the App, and repositories already containing a `dusk.md` appear.

That discovery has no upper bound, and the App does not fully control who installs it.

Registration through the manifest creates a private App, but its owner can make it public afterwards in GitHub's settings, and an organisation admin can install an organisation-owned App onto any repository in that organisation.
So the set of installations is not the set the operator chose.

The consequence is worse than unwanted noise in a catalog.
[ADR-0014](0014-agent-context-injection.md) injects catalog content into agent context at boot, so a `dusk.md` in a repository Dusk never meant to read becomes text an agent treats as fact and as instructions.
[ADR-0009](0009-proof-tokens.md)'s proof tokens govern writes, and nothing in the design governs whose *reads* enter the graph.

An uninvited installation is therefore a prompt injection path with a supply chain attached, and the poll floor would keep it current.

## Considered Options

1. **Trust every installation**, on the grounds that installing requires deliberate action.
2. **Approve each repository** in the UI before it is reconciled.
3. **Allowlist accounts**, defaulting to the account the App belongs to.

## Decision Outcome

Chosen: **option 3**.

An installation is reconciled only if its account is allowed.
The allowlist defaults to the account the App itself belongs to, captured during onboarding, and is widened with `DUSK_ALLOWED_ACCOUNTS`.

Both entry points check it.
A sweep skips a disallowed installation entirely, and a webhook delivery for one is refused, so neither the periodic path nor the event path can be the way in.

The check is on the **installation's** account rather than the repository's owner, because the installation is what Dusk has a relationship with, and it is the thing a stranger creates.

Refusals are logged rather than passed over quietly, per [`docs/philosophy.md`](../docs/philosophy.md).
An operator who genuinely wanted that installation needs to be told why nothing appeared, and one who did not needs to know it happened.

The default matters as much as the mechanism.
Requiring configuration to be safe means the deployments that never configure anything are the vulnerable ones, and those are the majority.

## Consequences

### Good

- The catalog's contents are bounded by something the operator controls, rather than by who has found the App.
- Safe with no configuration, which for [ADR-0027](0027-design-target.md)'s single operator is the only default that will actually be running.
- One check covers both triggering paths, so there is no route where the sweep enforces a rule the webhook does not.
- An unexpected installation becomes a log line rather than silent contamination, which is also how an operator finds out somebody installed their App.

### Bad

- Multi-account use needs configuration, and the failure mode is silence in the UI plus a warning in the logs, which is exactly where somebody will fail to look.
- The allowlist is per account, not per repository, so allowing an account allows every repository in it that carries a `dusk.md`. Within a trusted organisation that is still a wide door.
- An explicit `DUSK_ALLOWED_ACCOUNTS` replaces the default rather than extending it, so setting it without listing the App owner silently stops reconciling the owner's own repositories.
- It does nothing about a compromised repository inside an allowed account, which is the more likely attack once this one is closed. Content from an allowed repository is still trusted completely.
- Restricting by account is a coarse instrument in front of a real problem: catalog content is fed to agents without any trust boundary of its own. That boundary is still missing, and this ADR only narrows who can reach it.

### Rejected because

- **Option 1** was rejected because "installing requires deliberate action" describes the attacker as much as the operator. The action is deliberate; it is just not the operator's.
- **Option 2** was rejected as the wrong default rather than a bad idea. Per-repository approval is stricter and is worth having later, but it makes the empty configuration an empty catalog, which turns the cold start ADR-0004 designed into a chore and pushes people toward approving in bulk without looking.
