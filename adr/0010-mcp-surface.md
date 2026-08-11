# 10. The MCP surface is a few fat tools, not a mirror of the schema

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Agents are the primary consumer of Dusk, so the MCP surface is the product's main interface, not an integration detail.

The common failure mode is exposing one tool per schema operation, producing thirty or forty granular tools.
Agents handle that badly: answering one question costs a dozen calls, and the tool list itself consumes context before any work happens.

The opposite failure is a single `query` tool that takes a DSL, which pushes all the difficulty onto the agent and is impossible to use without documentation the agent does not have.

Separately, three mechanics need homes: how agent writes become commits, how notes avoid duplicating, and how an agent learns the open vocabulary that [ADR-0007](0007-entity-schema.md) permits.

## Considered Options

1. **CRUD per type**, roughly one tool per schema operation.
2. **A single query tool** accepting a DSL.
3. **A small set of composable, deliberately fat tools.**

## Decision Outcome

Chosen: **option 3**.

### Read tools

- `search(query, kind?, limit?)`: full-text over entities **and notes together**, backed by FTS5. The workhorse. "How do I reach the zwave pi" is a note and "the zwave pi" is an entity, and one search must find both.
- `get(ref)`: everything about one entity. Declared facets, observed facets, relations, attached notes, current drift. Deliberately fat, because an agent asking about an entity wants the whole picture rather than five follow-up calls.
- `neighbors(ref, relation?, direction?, depth?)`: graph traversal.
- `changes(since?)`: what moved recently, for an agent picking up context.

### Write tools

- `declare(ref?, fields, unset?)`: with an id it is an update, without one it is a create.
- `note(refs[], body, kind)`: attach knowledge to entities.
- `relate(from, to, type)`: typed edges.
- `mintKind(namespace, name, aliases?)`: extend the vocabulary.
- `push()`: flush the session's queued commits.

Every write requires a proof token per [ADR-0009](0009-proof-tokens.md).

### Four rules

1. Every read returns refs that feed directly back into `get`. Composability over completeness.
2. Reads return markdown wherever a human-readable answer exists, not deeply nested JSON. Agents reason better over prose.
3. Writes report where they landed: repo, path, and commit or pull request URL. An agent must be able to hand a human a link rather than saying "done."
4. In read mode, writes return the proposed diff instead of failing, which is what makes read-only first class per [ADR-0005](0005-github-app-and-access-modes.md) rather than a broken-feeling degradation.

### Commit queue

One commit per write call, queued on a **per-session branch**.

Per-session branches mean concurrent agents cannot drag each other's half-finished work into a push, and both write modes fall out of one mechanism: direct mode fast-forwards, proposal mode opens a pull request from that branch.

`push()` flushes the queue.
Sessions that end without pushing are flushed automatically, and abandoned branches are swept after a timeout, because a queue nobody flushes is silent data loss.

The queue is a real branch in a local clone, so it survives a restart.

Commit messages generate from the call, producing a readable history: `note: add gotcha to scrypted`.

### Note kinds

Notes carry a kind drawn from the same `getKinds` and `mintKind` machinery as entity kinds, in a separate namespace.
Well-known values seed the set: `gotcha`, `runbook`, `howto`, `decision`, `incident`, `todo`.

Kind affects **ranking and rendering**, not just labelling.
A `gotcha` surfaces prominently on an entity page and ranks highly in how-do-I search; a `todo` does not pollute those results.
A purely decorative kind would be chosen carelessly and would be worthless.

### Note identity and dedup

Notes carry ids, returned on write and included in `get`, so an agent can update rather than duplicate.

Because an agent with no memory of a prior session cannot know an id, two further layers apply: an identical body on the same entity returns the existing id rather than creating a second note, and a near-identical body is created with a warning naming the similar note. FTS5 makes the similarity check nearly free.

## Consequences

### Good

- A handful of tools fits in an agent's context and can be described adequately in the instructions field.
- The fat `get` means the common question costs one call rather than five.
- Searching entities and notes together matches how questions are actually asked.
- Per-session branches make concurrency, review, and both write modes fall out of one mechanism rather than three.
- Returning the landing location means agents can hand humans links, which is the difference between a verifiable claim and a bare assertion.

### Bad

- Fat responses cost context on every call, including when the agent wanted one field. This is a deliberate bet that fewer richer calls beat more granular ones, and it is annoying to reverse once agents depend on the shape.
- One commit per call produces a verbose history. Readable messages mitigate it, but a long session still yields many commits.
- Automatic flush on session end requires reliable session-end detection, which is not always available.
- Three dedup layers is more machinery than any one of them, and near-duplicate warnings will sometimes fire on notes that are legitimately similar.

### Rejected because

- CRUD per type was rejected because it makes the agent assemble answers from many calls, which is slow, expensive, and error-prone.
- A single DSL query tool was rejected because it moves the difficulty to the caller. An agent without documentation cannot construct a correct query, and the tool description cannot carry a whole language.
