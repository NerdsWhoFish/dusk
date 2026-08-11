# 14. Agent context is injected three ways, and scoped to Dusk itself

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

An MCP server is passive.
The agent has to decide to call it, which means Dusk's value depends on the agent already knowing Dusk exists and knowing what is in it.

There is a harder dependency too.
The read-before-write invariant in [ADR-0009](0009-proof-tokens.md) is unusual enough that an agent meeting it cold will fail every write and not understand why.
The write contract has to reach the agent before its first write attempt, or the product does not function.

The MCP specification provides an `instructions` field on the initialize result, which clients surface to the model.
That field is returned in the initialize **response**, before the client has communicated anything about the session, including filesystem roots.
It is therefore static and location-blind by construction.

## Considered Options

1. **`instructions` only.** Portable, but cannot be tailored to where the session is.
2. **A client hook only.** Location-aware, but works in one client and nowhere else.
3. **All three: `instructions`, a `dusk_context` tool, and an optional hook.**

## Decision Outcome

Chosen: **option 3**, layered so that each is an accelerator over the one below.

- **`instructions`** carries the portable, location-independent half: how to use the tools, the write contract, available kinds, and a names-only inventory. It ends by telling the agent to call `dusk_context`.
- **`dusk_context(root)`** is an ordinary tool. Any MCP client and any agent can call it. Dusk maps the root to a repo in its catalog and returns tailored context, including whether that repo has a `dusk.md` and which entities it owns.
- **A client hook** calls `dusk_context` automatically at session start so the agent does not have to remember. Claude Code gets this for free; every other client degrades to compliance.

MCP `roots` with `listChanged` is used where available, so context can be re-tailored if the session's roots change.

### Scope: Dusk and inventory, not knowledge

The injected content is an **interaction manual and an inventory**, not a knowledge dump.
Project `CLAUDE.md` and `AGENTS.md` keep doing their job, and Dusk does not attempt to replace them.

The moment this becomes "put your whole CLAUDE.md in here", it is a worse version of a file that already works.

### It is a page

The injected content is a page in the layout system of [ADR-0013](0013-layout-and-pages.md), whose render target is an agent's context rather than a browser.

Users customise it by editing markdown in the config repo.
Pinning notes is a block with a query.
Changes go through the normal write path, so in proposal mode an agent editing it opens a pull request.

That review path matters more here than anywhere else in the system.
A bad note is wrong in one place; **bad instructions poison every future session.**

### A token budget with visible truncation

The rendered page has a hard token ceiling, starting around 2k.

People will pin too much, because pinning is free to them and the cost lands on every future session.
The failure is invisible: agent quality degrades and nobody connects it to the page.

Exceeding the budget truncates and warns in the UI.

## Consequences

### Good

- The write contract reaches the agent through the one channel that is guaranteed to be delivered, so the unusual invariant is teachable rather than a wall.
- Dusk becomes proactive rather than passive. An agent starts a session already knowing what exists.
- Three layers degrade gracefully. Full behaviour in Claude Code, near-full in any MCP client, useful even if the agent ignores the prompt to call `dusk_context`.
- Reusing the page model means no new representation, no new editor, and no new review path.
- An explicit budget makes a silent failure visible.

### Bad

- Three delivery paths is three implementations of roughly the same idea, and they can drift.
- The hook is client-specific and will need maintaining against a moving target.
- `instructions` cannot be updated mid-session, so anything stale stays stale until reconnect.
- A token budget means content is dropped, and choosing what to drop is a policy decision that will be wrong for somebody.
- Every session pays the context cost whether or not it ever touches Dusk.

### Rejected because

- `instructions` alone was rejected because it cannot be location-aware, and location is what makes the context genuinely useful rather than generic.
- A hook alone was rejected because it would make Dusk work properly in exactly one client, which contradicts the portability the MCP surface is built for.
