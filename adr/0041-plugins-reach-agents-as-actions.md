# 41. A plugin reaches agents through the tools that already exist

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

Dusk is meant to be a portal an agent works through, not only a catalog it reads.
A Spacelift plugin should create and manage stacks; a Home Assistant plugin should run an automation. That means a plugin's capabilities have to reach agents over `/mcp`, and a plugin has to be configurable by an agent rather than only through a form.

The obvious implementation is that a plugin contributes MCP tools and Dusk registers them.
[ADR-0010](0010-mcp-surface.md) exists to prevent exactly that, and says why in its problem statement: "exposing one tool per schema operation, producing thirty or forty granular tools. Agents handle that badly: answering one question costs a dozen calls, and the tool list itself consumes context before any work happens."

Dusk deliberately ships a handful of fat tools.
Five installed plugins contributing four tools each is twenty more, paid for by every agent on every session before any work happens, and it arrives at the failure ADR-0010 rejected from a direction ADR-0010 did not consider.

The tension is real: the tool budget is a fixed resource, and [ADR-0042](0042-installing-plugins.md) makes the number of installed plugins unbounded.

## Considered Options

1. **Direct registration.** A plugin declares MCP tools and Dusk registers each one.
2. **One tool per plugin.** Each plugin gets a single namespaced tool taking an operation and parameters.
3. **No new tools.** A plugin's capabilities reach agents through the tools Dusk already has.

## Decision Outcome

Chosen: **option 3**. The MCP surface does not grow when a plugin is installed.

### A plugin capability is an action, not a tool

`PluginService` already carries everything a tool would need, because [ADR-0015](0015-plugin-actions-and-events.md) built it: `Describe` returns `ActionDescriptor` with a name, description, JSON Schema parameters, an `ActionClass`, and `proof_from` naming the read that satisfies [ADR-0009](0009-proof-tokens.md)'s read-before-write contract. `Invoke` runs one. `DryRun` previews it.

That is a tool declaration and a tool call under another name.
Treating plugin tools as a second concept would mean two registries, two permission models and two things to document, for one behaviour.

So agents reach plugin capability through `invoke`, and discovery folds into `get`, which is already "deliberately fat, because an agent asking about an entity wants the whole picture rather than five follow-up calls". What can be done to a thing is part of the picture of that thing.

The surface is constant: installing a tenth plugin adds no tools, and an agent that has read an entity already knows what it can do to it.

### One declaration, three surfaces

The same `ActionDescriptor` becomes a button in the UI ([ADR-0020](0020-plugin-ui.md)), an invocable capability over MCP, and an approval gate. A plugin author declares an action once and does not choose an audience.

### Configuration over MCP, except the secrets

[ADR-0023](0023-plugin-configuration.md) already splits configuration by sensitivity: non-sensitive values are markdown frontmatter in the config repository, sensitive values live in the encrypted store and are write-only.

That split decides this one. Non-sensitive configuration is *already* a file in git, so an agent configuring a plugin is an agent editing a declaration, which is `declare` doing what it does, through the same proof tokens and the same commit trail.

**Sensitive fields are never settable over MCP.** A secret passed as a tool argument is a secret written into an agent's context, its transcript, and any log along the way, and encrypting it on arrival does not unwrite it from the four places it has already been. Those fields are entered in the UI, which is the surface that can accept a value without recording it.

## Consequences

### Good

- The MCP surface is constant no matter how many plugins are installed, so ADR-0010's budget survives contact with an unbounded marketplace.
- One `ActionDescriptor` serves agents, the UI, and approval, so a plugin author declares a capability once.
- Configuring a plugin over MCP inherits git review, history and proof tokens for free, because it was already a file.
- Secrets have exactly one entry path, which is the one that does not persist them anywhere an agent can reach.

### Bad

- **An agent cannot see a plugin's capabilities from the tool list**, which is where agents look first. It has to read an entity to learn what can be done to it, and an agent that never calls `get` never discovers that anything is possible. This is the direct cost of the decision and the strongest argument for option 1.
- `invoke` becomes a wide tool with a schema that depends on the action, which is closer to ADR-0010's rejected DSL tool than anything else in the surface. It is bounded by actions being declared with JSON Schema rather than free-form, but the resemblance is real.
- A plugin author who has written MCP servers will expect to contribute tools, will not find that, and will experience the constraint as a limitation rather than a budget.
- Fully configuring a plugin needs a human at a UI whenever it has a credential, so "an agent sets this up" is only ever partly true.

### Rejected because

- **Option 1** was rejected because it reproduces the exact failure ADR-0010 was written to prevent, with the number of tools now set by how many plugins somebody installed rather than by anyone's design.
- **Option 2** was rejected because it grows the tool list linearly with plugins, more slowly than option 1 but without bound, and each such tool is a small DSL of the kind ADR-0010 rejected as option 2 for pushing difficulty onto the agent.

## Amendments

### 2026-08-13: configuration is a `configure` tool, not `declare`

"Configuration over MCP, except the secrets" reasoned from a premise that turned out to be false: non-sensitive configuration was **not** already a file in git, so there was no declaration for `declare` to edit.
[ADR-0043](0043-plugin-configuration-stays-out-of-git.md) explains why it stays out of git rather than being moved there.

An agent configures a plugin through a single fixed `configure` tool, which merges over what is stored rather than replacing it.
The decision this ADR exists to make is unchanged: the surface does not grow when a plugin is installed, and one more fixed tool is not growth with the marketplace.
Sensitive fields are still never settable here, for the reason given above, and are now refused by name rather than accepted and silently dropped.

### 2026-08-19: discovery gets a fixed tool, on the same reasoning

The first of the bad consequences above, that an agent cannot see a plugin's capabilities from the tool list, fired twice on a real catalog and was patched twice with another sentence inside `dusk_context`.
[ADR-0077](0077-a-plugin-is-discoverable-from-the-tool-list.md) pays it with one fixed `plugin` tool, the same shape and the same reasoning as `configure` in the amendment above.

Option 1 and option 2 stay rejected.
What changes is only that discovery no longer lives exclusively inside `get`, because `get` needs a ref and no ref names the set of installed plugins.
