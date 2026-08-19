# 77. A plugin is discoverable from the tool list

Date: 2026-08-19

## Status

Accepted. Amends [ADR-0041](0041-plugins-reach-agents-as-actions.md).

## Context and Problem Statement

[ADR-0041](0041-plugins-reach-agents-as-actions.md) decided that a plugin's capability reaches agents as an action rather than as a tool, so the MCP surface does not grow with the marketplace.
It wrote down the price of that decision itself, as the first of its bad consequences:

> **An agent cannot see a plugin's capabilities from the tool list**, which is where agents look first.
> It has to read an entity to learn what can be done to it, and an agent that never calls `get` never discovers that anything is possible.
> This is the direct cost of the decision and the strongest argument for option 1.

That consequence has now fired twice on a real catalog, and both times it was patched with another sentence in the injected context rather than answered.

[ADR-0057](0057-charged-for-what-was-printed.md) added one sentence saying the catalog acts, because nothing in the orientation mentioned `invoke`.
[ADR-0076](0076-the-context-carries-the-manual-for-the-tools-it-names.md) then amended itself to add a second, because an operator with an installed, running, enabled plugin offering eight actions watched an agent report that the capability did not exist.
Both fixes live inside the body of one tool's answer.
An agent reaches them only by calling `dusk_context` and reading far enough, and the failure being fixed is an agent that has already concluded there is nothing to look for.

Three further gaps showed up while checking that one.

**There is no ref for "every plugin".**
`get` answers about one thing named by a `kind:namespace/name` ref, and `plugin:<name>` is a ref of that shape.
The roster is not: no ref names the set, so the question "what integrations does this operator have" is one the catalog's shape cannot express, and no tool asks it.
`changes` lists plugin health, which is the answer to "should I trust this", not to "what can be done here".

**What a plugin observes has never been visible on any agent surface.**
`emits_kinds` has been in `DescribeResponse` since the protocol was written, and every shipped ingesting plugin populates it.
Dusk reads it nowhere.
The plugin page even says *"Anything it does is what it observes"* to a plugin with no runnable actions, and then does not say what it observes, which is the whole of what that plugin is for.

**The manual names some of the tools this deployment registered.**
ADR-0076 required that the manual never name a tool the server did not register, and tested that direction.
The other direction was never stated, so `page` has been registered and unnamed since it was built, and any tool added later inherits the same silence.

## Considered Options

1. **Leave it to `dusk_context`**, and add a third sentence.
2. **A tool per plugin**, namespaced, taking an operation and parameters.
3. **One fixed `plugin` tool**: the roster with no argument, one plugin whole with a name.
4. **Fold the roster into `changes`**, which already reports plugin health.

## Decision Outcome

Chosen: **option 3**. A single fixed `plugin` tool, which answers with the roster when given nothing and with one plugin when given a name.

### The surface stays constant, which is the property ADR-0041 defends

ADR-0041's decision is that the tool count is not set by how many plugins somebody installed.
One more fixed tool does not move that: the surface is the same with one plugin installed and with thirty.
[ADR-0043](0043-plugin-configuration-stays-out-of-git.md) already settled this shape when it gave configuration a fixed `configure` tool, saying so in ADR-0041's own amendment: *"one more fixed tool is not growth with the marketplace."*
This is that reasoning applied to discovery, which is the half ADR-0041 left unpaid.

So ADR-0041 is amended rather than superseded.
Option 1 and option 2 are still rejected, for the reasons it gave.

### The tool list is where an agent looks, so the answer goes there

A tool description is loaded before any work happens and is visible without a call.
That is what makes it the right place for the one fact ADR-0041 could not deliver: that integrations exist here at all.
`dusk_context` still carries the roster, because the orientation is what an agent reads at session start, and the tool is what it reaches for once it has a question.

### What the roster says

Per plugin: its id, its version, whether it is running, **what kinds it puts in the catalog**, and its enabled actions with the scope each one takes.
That is both halves of what a plugin is: what it observes and what it can be told to do.
`emits_kinds` is read for the first time here, and on the plugin page.

An action that is declared and not enabled is not capability ([ADR-0015](0015-plugin-actions-and-events.md)), so the roster counts it separately rather than offering it.
A plugin is listed whether or not it has any, because "installed and observing" is an answer, and the narrowing ADR-0076 used, only plugins with an enabled action attached to no kind, was a workaround for having no roster.

### `get plugin:<name>` keeps working, against one renderer

`plugin` is the front door and `get plugin:<name>` delegates to the same code.
Retiring it was rejected: ADR-0076's amendment shipped the context pointer that names it, and an agent holding a `plugin:` ref from any earlier session would hit a wall for no gain.
The overlap is on the singular case only, and it is one implementation, so the two cannot drift.

### The manual names every tool this deployment registered

ADR-0076's rule becomes an equivalence rather than an implication: the manual names a tool if and only if the server registered it.
A test drives the server, asks it for its own tool list, and fails if the two disagree in either direction.
That is what makes the rule survive the next tool, instead of being remembered.

`dusk_context` itself stays out of the table, because it is the call the reader is already inside, and the server instructions name it.

## Consequences

### Good

- The fact ADR-0041 could not deliver, that this deployment has integrations and they do things, is now in the tool list, which is the one surface an agent sees without asking.
- The roster is a question that had no expression: `get` needs a ref and no ref names the set.
- `emits_kinds` reaches a reader for the first time, so a plugin that only observes stops being described as a plugin with nothing to run.
- The manual's completeness is checkable rather than remembered, and it caught `page`, which had been registered and unnamed since it was built.
- ADR-0076's narrowing goes away: a plugin is named because it is installed, not because its actions happen to attach to no kind.

### Bad

- **One more tool on a budget [ADR-0010](0010-mcp-surface.md) exists to protect.** The tool list is spent on every session whether or not a plugin is ever asked about, and a deployment with no plugins does not register it, which means the cost lands exactly on the deployments that have the most other things to pay for.
- Two entry points reach one plugin page. One renderer keeps them identical, but a reader still meets the same answer under two names.
- The roster restates what `dusk_context` now also carries. That duplication is deliberate, on ADR-0076's own reasoning about descriptions and context being read from different states, and it is still duplication that can drift in wording.
- `emits_kinds` is a **claim by the plugin**, not an observation. A plugin that declares a kind and emits none reads as covering it, and Dusk cannot tell without asking the index what that plugin actually wrote, which is a different question with a different cost.
- The context now names every installed plugin rather than a filtered few, so a deployment with many pays more of a budget [ADR-0050](0050-what-the-context-budget-buys-first.md) reserves for pinned knowledge. The list is capped and names what it dropped, per [ADR-0059](0059-what-a-list-may-not-leave-unsaid.md), which bounds the cost without removing it.

### Rejected because

- **Option 1** is the fix that has already been applied twice and has already failed twice, in both cases for the same reason: it is reachable only from inside an answer the failing agent never asked for.
- **Option 2** grows the tool list without bound and reproduces what ADR-0010 and ADR-0041 both rejected. Nothing about it has changed.
- **Option 4** conflates two questions. `changes` answers "how much should I trust what you just told me", and a roster is "what can be done here"; ADR-0010 separated `drift` from `changes` on exactly that distinction, between an answer's soundness and a thing to go and do.
