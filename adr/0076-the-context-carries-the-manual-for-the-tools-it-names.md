# 76. The context carries the manual for the tools it names

Date: 2026-08-18

## Status

Accepted. Extends [ADR-0014](0014-agent-context-injection.md) and narrows [ADR-0071](0071-mcp-results-are-dual-purpose-and-bounded.md).

## Context and Problem Statement

[ADR-0014](0014-agent-context-injection.md) describes the injected context as "an interaction manual and an inventory".
The inventory half was built and maintained. The manual half was never written as a block, and decayed into sentences scattered through the other sections: one about refs at the end, one about note ids added later, one about actions above them, one about what absence means below.

An operator watched an agent conclude that **Dusk has no way to record an architecture decision**.
`decision` is a note kind. It has always been a note kind.
But it holds zero notes, and nothing in the injected context, the server instructions, or any tool description ever names the note kinds, so the only way to discover it is to call `kinds`, which an agent has no reason to call once it has concluded the capability is missing.
`incident` is in the same position, and any future kind will be.

That is a discovery failure with a specific shape: **the vocabulary is data, and it was being treated as documentation.**
Every other fact the context carries is read out of the catalog. The one an agent most needs in order to write anything back was hardcoded nowhere and rendered never.

Two smaller faults showed up in the same review, both of them repetition the reader pays for on every line:

- A note the budget could not print whole was named, and each line ended `(not shown; ask note for it)`. Thirty named notes stated that convention thirty times.
- A kind whose entity names did not fit ended `names not listed, search finds them`, once per kind, in a section whose entire purpose is that `search` exists.

And the note's opening line, which [ADR-0031](0031-notes-are-files.md) makes its stand-in for a title, was inserted into a list with its markdown heading marker still attached, so the answer was full of `- **gotcha** · id: # Some Title`.

## Considered Options

1. **Put the manual in the server `instructions`.**
2. **Put it in each tool's description**, where a client already pays for it.
3. **Put it in `dusk_context`, generated from what this deployment actually registered and what the vocabulary actually holds.**
4. **Leave discovery to `kinds`**, and treat this as an agent that did not look.

## Decision Outcome

Chosen: **option 3**, with a hard rule about what may appear in it.

`dusk_context` gains one `## Working with this catalog` block: a table of calls, the ref and note-id shapes, the proof-token rule, and the note kinds **grouped by role with their live counts**, under a sentence saying that knowledge of every sort is a note told apart only by kind, so a decision and an incident go through `note` rather than a tool of their own.

**The table lists only what this deployment registered.** The conditions are the registration conditions: `declare` and `relate` appear when there is a writer and a token source, `invoke` and `configure` when there are plugins. [ADR-0057](0057-charged-for-what-was-printed.md) already refused to mention actions on a deployment with no plugins, for the reason that naming a tool the server never added sends an agent at something that is not there. This generalizes that from one sentence to the whole manual, and a test holds it.

The repetition is stated once in that block and removed from every line that carried it.
A named note is now a list item carrying its kind, its id and its title, and a counted kind is `- **service** (74)`.
The heading markers come off the title.

**Option 1 was rejected on cost and on truth.**
The instructions are spent on every session whether or not the catalog is ever consulted, and [ADR-0071](0071-mcp-results-are-dual-purpose-and-bounded.md) caps them at 700 bytes for that reason. They are also fixed at compile time, so they cannot name this deployment's note kinds or omit a tool it did not register, which are the two things that made the manual worth having. They are instead tightened to point at `dusk_context` as the call that carries the manual, while keeping the three facts that must survive an agent never making that call: search before guessing, refs feed `get`, and knowledge of every sort is a note.

**Option 2 was rejected because it answers a question nobody asked.**
A tool description is read when an agent is already looking at that tool. The failure here was an agent not knowing a capability existed at all, and no description of `note` is reachable from that state.

**Option 4 was rejected as blaming the reader for a discoverable-in-principle answer.**
Dusk's own product claim is that absence means nobody wrote something down. An agent applying that rule to a kind list it was never shown is reasoning exactly as instructed.

## Consequences

### Good

- The capability that was invisible is now named, with its count, in the first call of every session. A kind added later appears without anyone remembering to document it.
- The manual cannot advertise a tool this deployment does not have, and a test fails if it does.
- Removing the per-line repetition paid for most of the block: the inventory's counted form went from roughly sixty bytes a kind to twenty, and against a real catalog the whole answer moved from 6,542 to 7,784 bytes inside an 8,000 budget.
- More kinds now fit, because each costs a count and a name rather than a sentence.
- The answer is markdown a reader can skim: one table, one list per role, list items that do not restate each other.

### Bad

- **The context spends about 1,200 bytes on a manual that never changes**, and pays it in every session, including the ones that would have been fine without it. That is real budget taken from pinned notes and the inventory, which is what [ADR-0050](0050-what-the-context-budget-buys-first.md) says the budget is for.
- It is in the tail, which is reserved before anything is spent, so it is the one part that cannot be given up when a catalog is large. A catalog big enough to need every byte pays it first.
- The manual restates what tool descriptions already say, which is the duplication [ADR-0071](0071-mcp-results-are-dual-purpose-and-bounded.md) removed from the instructions. It is accepted here because a description is only read once a tool is already in hand.
- Two places now describe the tool surface, and they can drift. Only the registration conditions are shared; the wording is not.
- A test asserting the old wording of every heading and overflow line had to change, which is the cost of putting prose under test at all.

### Rejected because

- **The instructions** cannot name a deployment's kinds or omit its missing tools, and are capped at 700 bytes precisely because they are unconditional.
- **Tool descriptions** are unreachable from the state that caused the failure, which was not knowing the tool was the answer.
- **Leaving it to `kinds`** asks an agent to call a tool to disprove a conclusion it has already drawn, using the reasoning rule Dusk itself taught it.
