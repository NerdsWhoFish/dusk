# 69. Agent context is operator-configured in Git

Date: 2026-08-18

## Status

Accepted.

## Context and Problem Statement

The injected context had one hard-coded policy: pinned notes first, then declarations and inventory under an 8000 byte ceiling.
That is a useful default, but the operator had no way to ask for the whole inventory and no notes, reorder kinds, change the budget, or add instructions shared by every agent.

The policy is durable operator knowledge and belongs beside the catalog, not in a server environment variable or a client hook.

## Considered Options

1. Add `.dusk/context.md` to the config repository.
2. Put context policy in deployment environment variables.
3. Let each client configure and assemble its own context.

## Decision Outcome

Chosen: **option 1**.

`.dusk/context.md` uses `dusk: context/v1` frontmatter to configure the byte budget, section order, inventory detail, and kind order.
Its markdown body is injected as operator instructions.

The reconcile validates and materializes the profile with the graph.
A malformed profile fails the reconcile instead of silently falling back, and `dusk_context` reads it from SQLite rather than spending a GitHub API request on every session.

The file is optional.
Without it, the existing ranking and 8000 byte budget remain unchanged.

## Consequences

### Good

- The human and every agent receive one versioned, reviewable orientation policy.
- Clients remain dumb transports and cannot drift in ranking or truncation.
- Session start adds no GitHub API traffic.

### Bad

- Another reserved file exists under `.dusk/`.
- The profile is intentionally small rather than a general page query language.
- Instructions consume the same budget as catalog facts and can crowd them out.

### Rejected because

- **Option 2** was rejected because multiline policy is miserable to review in environment variables and has no useful history.
- **Option 3** was rejected because every client would become a second content-policy implementation, contradicting the hook's pass-through contract.
- Reusing `.dusk/home.md` was rejected because a portal layout and agent orientation have different consumers, budgets, and failure modes.
