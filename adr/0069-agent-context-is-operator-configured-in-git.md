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

## Amendments

Amendment policy: [ADR-0028](0028-amending-adrs.md).

### 2026-08-18: the budget reached the backstop and never the allocation

This ADR gives the operator the context budget. Half of that was built.

`assemble` divided a compiled-in constant between the sections, and only `truncate` was passed the operator's number.
So a budget **below** the default worked, by cutting the answer after it was built, and a budget **above** it did nothing at all: the sections were still allocated 8,000 bytes, and truncating that at 16,384 is a no-op.

It hid well because both failures look like success. Nothing errors, the profile validates, and the answer that comes back is a perfectly good answer of exactly the size it was always going to be. The operator who raised the budget to fit their pinned notes got the same bytes back and no reason to doubt the setting. It was found by raising a real one and measuring: 7,816 bytes at a budget of 16,384.

Two constants naming the same ceiling is what let them drift. `mcp.ContextBudget` is now an alias of `contextconfig.DefaultBudget` rather than a second 8,000, and `assemble` takes the ceiling as an argument.

A test asserts the property rather than the plumbing: doubling the budget must return more than the default did. Getting it to fail correctly took two attempts, and both are the reason it is worth writing down. Comparing a *smaller* budget against the default proves nothing, because truncation shrinks the answer whether or not the allocation moved. And one long note proves nothing either, because a section packs greedily and an item is all-or-nothing, so a note too large for both budgets degrades identically under each. The fixture is many medium notes, where the extra room changes how many arrive whole.
