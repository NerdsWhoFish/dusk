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

## Amendments

Amendment policy: [ADR-0028](0028-amending-adrs.md).

### 2026-08-16: the hook is a binary on `SessionStart`, and every failure of it is silent

This decision named three injection paths and built two.
The third was described in one sentence and its shape left open, so what follows fills that in rather than deciding anything new.
The layering stands, the scope stands, and the budget stands: the hook is still an accelerator over `dusk_context`, and nothing on the MCP surface may assume it ran.

**It is a `SessionStart` hook**, which is one of only three Claude Code events whose output reaches the model's context, alongside `UserPromptSubmit` and `UserPromptExpansion`.
On every other event what a hook prints goes to a debug log the agent never reads, so a hook installed on the wrong one runs, exits zero, and injects nothing.
That is the constraint that picks the event, and it is not obvious from the outside: the failure looks like a working hook.

`UserPromptSubmit` reaches the context too, and was rejected for paying the budget once a *turn* instead of once a session.
This ADR already records that every session pays the context cost whether or not it touches Dusk, and per turn multiplies exactly the cost it was worried about.

**It ships as `dusk-context`, a binary in this repository**, installed by naming it as a `SessionStart` command in the client's settings.
The logic is `pkg/contexthook` and the command is the thin wrapper [ADR-0017](0017-engineering-policy.md) asks for, which is what makes the hook testable at all.

**It passes the payload's working directory to `dusk_context` and passes the answer back unchanged.**
Ranking, the budget and what is dropped to stay inside it are decided server side ([ADR-0050](0050-what-the-context-budget-buys-first.md), [ADR-0057](0057-charged-for-what-was-printed.md)), so a client that edits the answer is a second content policy that nobody can see and nothing tests.
Exactly one field of the hook payload is decoded, because the rest of that schema is the moving target this ADR accepted as a cost, and a field never read cannot break.

**Configuration is two environment variables and nothing else**: the endpoint, and the bearer token under the same name the server requires it as.
A hook is installed by an entry in a settings file, and settings files are committed, so a token written where the hook is wired up is a token in somebody's git history.

**Every failure is silent and exits zero**, with one line on standard error, which a client shows only in its debug log and a person sees immediately by running the binary by hand.
Unreachable, unauthenticated and unconfigured are all the same answer: nothing on standard output.
This is the load-bearing rule, because the hook is installed once and fires in every repository, and a hook that errors where Dusk is irrelevant is worse than no hook.

It reads as a contradiction of [the philosophy's](../docs/philosophy.md) rule that anomalies are surfaced rather than silenced, and it is not.
That rule governs what the catalog *says*, so that a stale answer is never mistaken for a current one.
A hook that says nothing has made no claim to be wrong about, and the degradation is the one this ADR designed: `instructions` still tells the agent to call `dusk_context` itself.

#### Consequences of the hook

Good:

- The hook is a client of the same public tool every other agent uses, so it cannot drift from what a compliant agent gets. The three paths this ADR worried would drift now share one implementation for two of them.
- A failure costs a session its orientation and nothing else, which is what makes installing it globally reasonable.
- Running it by hand prints what a session in that directory would be given, so "is this wired up" is answerable without starting a session.

Bad:

- **The working directory is passed verbatim**, and Dusk matches it by suffix against `owner/name`. A checkout whose path does not end that way gets the estate-wide answer rather than the tailored one, and nothing says so. Reading the git remote would fix it and was rejected below.
- Silence means a misconfigured hook and a correct one look identical from inside a session. The debug log and a hand run are the only ways to tell.
- The payload and output shapes are asserted against a documented contract rather than against a real client, so a change to that contract fails silently and the tests still pass. That is the cost this ADR named, now concrete.
- A binary is a thing to install and keep current, on every machine an agent runs on.

#### Rejected for the hook

- **A shell script** was rejected on what the hook has to do rather than on taste. `/mcp` is JSON-RPC over streamable HTTP, so a script has to hand-roll the initialize handshake, carry the session header, and parse an event stream, in `curl` and `jq`, forever, against a spec it does not own. The module already carries an MCP client. The counter-argument is real and it is the only one: a script is trivially installable and a binary is not. It loses because [ADR-0017](0017-engineering-policy.md) puts logic in a package where it can be tested, and an untested script talking to a live service is exactly the thing that rots without anybody noticing.
- **A `type: "http"` hook**, where the client POSTs to a URL and Dusk answers, was rejected despite removing the binary entirely. It puts the client's payload and response schema inside the service, so the moving target this ADR accepted would live in Dusk rather than in a small program somebody can update on its own. That is the coupling "a hook alone" was rejected for, arriving through the server instead of the client.
- **Resolving the repository from the git remote** was rejected here and is worth revisiting. It would turn any checkout into the `owner/name` Dusk matches exactly, which is strictly better than a path that has to end in the right two segments. It also puts a second answer to "which repository is this" in the client, next to the one `dusk_context` already computes, and it means shelling out to `git` from a hook. Deferred rather than dismissed.
- **A subcommand of `dusk`** was rejected because `dusk` is the server: it carries SQLite, gRPC and the embedded UI, and installing it on a laptop to run a hook is a service binary in a place no service runs.
- **Caching the answer between sessions** was rejected because the catalog changes under it and the whole point is that the orientation is current. The exchange costs one round trip inside a five second deadline.
