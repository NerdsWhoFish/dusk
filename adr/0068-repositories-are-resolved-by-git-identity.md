# 68. Repositories are resolved by Git identity

Date: 2026-08-18

## Status

Accepted.

## Context and Problem Statement

The context hook sent a working directory and the server matched any catalog repository whose `owner/name` happened to be that path's suffix.
That guessed correctly only for one checkout layout, could match an unrelated directory, and stopped working after a repository rename or transfer.

GitHub gives a repository a stable numeric id, while its owner and name are mutable.
The local checkout already carries the exact remote it means.

## Considered Options

1. Keep suffix matching and document the expected checkout layout.
2. Resolve the checkout's GitHub origin in the hook and match the exact slug on the server.
3. Send the absolute path to GitHub and ask it to infer a repository.

## Decision Outcome

Chosen: **option 2**, with stable GitHub ids retained server side.

The hook asks Git for `remote.origin.url`, normalizes GitHub SSH and HTTPS remotes to `owner/name`, and sends that exact value.
The server never suffix-matches a path.

Each sweep records GitHub's repository id beside its current slug.
When the same id appears under a new slug, the materialized rows move to the new slug and the old slug becomes an alias.
An old checkout remote therefore resolves to the current repository until the remote is corrected.

## Consequences

### Good

- Tailoring cannot select a repository merely because a directory ends in the same two path components.
- Any checkout layout works.
- Renames and transfers preserve context and provenance without treating the repository as deleted and recreated.

### Bad

- The optional hook now depends on `git` being available.
- A rebuilt disposable index learns historical aliases only when it has observed the repository before and after the move.
- Non-GitHub origins are intentionally reported as unknown, because GitHub is Dusk's repository substrate.

### Rejected because

- **Option 1** was rejected because a filesystem convention is not repository identity and silent false matches are worse than an explicit unknown answer.
- **Option 3** was rejected because an absolute local path has no meaning to GitHub and would add a network call to every session start.
- Matching only the current slug was rejected because repository transfers are ordinary and GitHub already supplies the stable key needed to follow them.
