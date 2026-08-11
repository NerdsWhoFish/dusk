# 15. Plugins expose entity-scoped actions, and invocations emit events rather than notes

Date: 2026-08-11

## Status

Accepted, deferred to after the core ships

## Context and Problem Statement

Dusk answers two questions: where is this thing, and how do I do this thing.
Notes answer the second question in prose.
Actions answer it executably.

The motivating case is capability narrowing.
Giving an agent raw `kubectl` grants it everything including cluster deletion.
Giving it a curated set of declared actions such as "restart this service" is strictly safer, and the catalog already knows which entities exist and which plugin observed them.

This changes what Dusk is.
Today a compromise leaks architecture.
With actions, **Dusk holds credentials that can mutate infrastructure**, and the blast radius changes category.
The safety model is therefore the feature, not a footnote.

Separately, invocations produce a record. An earlier proposal stored these as notes, which was wrong: notes are curated knowledge and invocations are a firehose, and mixing them reintroduces the accretion problem [ADR-0009](0009-proof-tokens.md) exists to prevent.

## Considered Options

For invocation shape:

1. **Plugin-scoped**: `invoke(plugin, action, params)`.
2. **Entity-scoped**: `invoke(ref, action, params)`.

For invocation records:

1. Notes.
2. Events in the SQLite index.
3. Events emitted always, persisted by an optional time-series exporter.

## Decision Outcome

### Actions are entity-scoped

`invoke(ref, action, params)`.

The agent names the thing, not the plugin.
Dusk resolves which plugin owns that entity and routes accordingly, so the agent never needs to know the plugin topology and cannot target the wrong object by string.

Plugin-scoped actions exist for work that is not about a single entity, but entity-scoped is the primary shape.

### Actions declare a class

Read-only, idempotent-mutating, or destructive.
Class drives approval defaults, so installs do not hand-configure every action.

### Dry run is required, and may return nil

Every action must implement dry run.
Returning nil is a valid, explicit statement that preview is unsupported.

A required method with an explicit unsupported return beats an optional method, because it forces the author to consider it and lets Dusk know the capability definitively rather than inferring it from absence.

**Nil is surfaced at approval time.** "This action cannot be previewed" is what a human needs before approving something destructive.

### Enabling is explicit, and approval is its own axis

Declared actions are **denied by default**.
Enabling is a deliberate act at install time, because installing a plugin must not silently grant capability.

Approval mode is separate from the git access mode in [ADR-0005](0005-github-app-and-access-modes.md).
Write access to a repository and permission to restart a pod are different powers and must not share a setting.

### Mutating actions require a proof token

Per [ADR-0009](0009-proof-tokens.md), an agent cannot mutate something it has not read.

The action declaration names **which read yields a satisfying token**, and a rejected invocation returns that call.
Without it an agent meets an unusual protocol blind and flails.

### Slow actions are asynchronous

An invocation returns a handle, and status is queryable.

### Invocations emit events, not notes

Events are emitted always, as structured output.

Persistence is an **optional time-series exporter**.
Without one, events go to logs like any other service, and a bounded in-memory ring buffer backs a "what just happened" view in the admin UI.

Events are deliberately **not** stored in the SQLite index.
That store is disposable by contract in [ADR-0001](0001-git-as-source-of-truth.md) and [ADR-0008](0008-storage.md), rebuildable from git at any time.
Events cannot be rebuilt from git, so placing them there would put non-derivable data in a store whose defining property is that it can be discarded.

An event may be **promoted to a note** by deliberate act, when an invocation turns out to be knowledge worth keeping.

### Sequencing

The action declaration shape ships in the `v1alpha` proto now.
The implementation lands after the core reconciler and catalog work.

Retrofitting the declaration into a published contract is the compatibility trap [ADR-0007](0007-entity-schema.md) exists to avoid, but building a control plane before the catalog is trusted would be premature.

## Consequences

### Good

- Capability narrowing is a genuine security improvement over handing an agent raw tooling, and it is a real differentiator.
- Entity-scoped invocation removes a class of targeting error, because the target is a catalog ref rather than a free-form string.
- Class plus default-deny means safe defaults without per-action configuration.
- Required dry run makes preview a first-class capability rather than an afterthought that most plugins skip.
- Separating events from notes keeps the knowledge base curated, and separating them from SQLite keeps the index honestly disposable.
- Tier 2 of [ADR-0002](0002-plugin-protocol.md) now has a concrete purpose rather than a hypothetical one.

### Bad

- **Dusk becomes a privileged control plane.** It holds credentials capable of mutating infrastructure, and that is a materially larger thing to secure and operate than a catalog.
- The plugin contract roughly doubles, since plugins now ingest and act.
- Approval workflow, action classification, and async status are substantial surface that does not exist today.
- Without a time-series exporter there is no durable audit trail from Dusk itself, which is an accepted degradation and will surprise someone.
- Requiring dry run raises the cost of writing a plugin, including for plugins whose actions cannot meaningfully be previewed.

### Rejected because

- Plugin-scoped invocation was rejected because it forces the agent to learn plugin topology and to target objects by string.
- Notes as invocation records were rejected because they would bury curated knowledge under a firehose.
- Events in SQLite were rejected because that index is disposable by contract and events are not derivable, so the two have incompatible lifetimes.
