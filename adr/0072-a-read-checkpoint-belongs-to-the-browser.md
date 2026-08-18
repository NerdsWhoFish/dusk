# 72. A catalog read checkpoint belongs to the browser

Date: 2026-08-18

## Status

Accepted

## Context and Problem Statement

Dusk should tell its operator what changed since they last read the catalog.
The catalog knows the current commit of every repository, but it has no user accounts, no per-user state, and no durable history of earlier materialized graphs.
Adding any of those only for an unread marker would contradict [ADR-0027](0027-design-target.md): Dusk is built for one trusted homelab operator and their agents, not an organization whose readers need synchronized state.

The useful question is narrower than a field-level audit log.
When the repository commits Dusk has read change, the source material changed; when a read starts failing, the operator also needs to see that before treating the old catalog as current.
The UI needs a baseline it can compare with the current repository statuses, and changes must not disappear merely because the page was opened.

## Considered Options

1. Store a read checkpoint in the browser and advance it only when the operator marks the changes read.
2. Store one global checkpoint on the Dusk server.
3. Add accounts and durable per-user checkpoints.
4. Persist every catalog snapshot and compute a semantic diff between snapshots.

## Decision Outcome

Chosen: **store the read checkpoint in browser local storage and advance it explicitly**.

The checkpoint records each repository's Git ref, commit, participation state, and last error.
The home response carries the repository statuses with its existing payload, so answering this does not add a request waterfall to the first page.
The UI compares the current statuses with the checkpoint and keeps the changed sources visible until the operator chooses **Mark as read**.
The first visit establishes the baseline and says that the sources are unchanged rather than inventing a history the browser never observed.

This is deliberately a source-level answer.
It says which repository moved, appeared, disappeared, stopped participating, or started failing; it does not claim to know which entity fields changed inside a commit.

## Consequences

### Good

- The operator gets a truthful unread signal without accounts, sessions, or a second identity model.
- Opening or refreshing the page does not acknowledge a change accidentally.
- The answer survives a Dusk restart because the baseline belongs to the browser that read it.
- The home page remains one API request.

### Bad

- Two browsers have separate checkpoints, so acknowledging a change on one does not clear it on the other.
- Clearing browser storage forgets the baseline.
- A source-level commit transition says that something changed, not what changed inside the catalog.
- A browser that refuses local storage can render the catalog but cannot retain the checkpoint.

### Rejected because

- **One global server checkpoint** was rejected because an agent, a phone, and a laptop would clear each other's unread state, which is less honest than independent browser state.
- **Per-user server checkpoints** were rejected because they require exactly the account and multi-user machinery the product boundary excludes.
- **Persisting every catalog snapshot** was rejected because the SQLite index is intentionally disposable and rebuildable from Git, while an audit database would create a second durable store with retention, migration, backup, and repair obligations.
  Git already retains the exact source history when a field-level investigation is needed.
