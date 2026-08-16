# 10. The MCP surface is a few fat tools, not a mirror of the schema

Date: 2026-08-11

## Status

Accepted. Amended, see [Amendments](#amendments).

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

## Amendments

Amendment policy: [ADR-0028](0028-amending-adrs.md).

### 2026-08-11: commits go through the API, and write mode commits directly

This ADR described the commit queue as "a real branch in a local clone, so it survives a restart".
[ADR-0029](0029-reading-repositories.md) came later and chose the GitHub API over cloning for reads, on the grounds that a reconcile wants a handful of files and a clone fetches all of history to deliver them.

Writes now follow it: a commit is created through the API rather than in a checkout.
The stated reason for the local clone is better served this way, not worse, because a branch that lives on GitHub survives a restart more completely than one that lives on a disk Dusk might not have next time.

**Write mode commits straight to the repository's default branch**, one commit per call.
The per-session branch remains for proposal mode, where a pull request needs a branch to come from.

That split costs something this ADR had: a multi-call sequence is no longer atomic in write mode, so a failure partway leaves the earlier calls committed.
It buys back the whole queue lifecycle, including the automatic flush and the abandoned-branch sweep that this ADR itself described as guarding against "silent data loss".
An operator who chose write mode has already said they trust the agent, and a queue that must be swept to be safe is a worse trade for them than a commit that simply lands.

`push()` therefore has nothing to flush in write mode and reports what already landed.
It keeps its full meaning in proposal mode, where it is what opens the pull request.

### 2026-08-13: `getKinds` and `mintKind` ship as one `kinds` tool, and a mint carries a role

This ADR listed `getKinds` and `mintKind(namespace, name, aliases?)` as two tools.
They ship as one, `kinds`, which reads with no `mint` argument and writes with one.

`note` and `page` both landed in that shape after this ADR was written, for a reason that applies here unchanged: the read is what issues the proof token the write needs, so a separate read tool would look optional.
It is stronger for a vocabulary than for either of those.
The proof token for a mint is proof of having read the vocabulary being extended, so the near-match warning this ADR's own consequences call for is delivered by the very call that authorizes the mint.
An agent cannot invent `svc` without having been shown `service` first, and that only holds while the read and the write are one tool.
The tool count is also a product constraint this ADR set itself, and one tool is one rather than two.

A mint additionally carries a **role**, which this ADR did not have.
It is what makes "kind affects ranking and rendering, not just labelling" true of a minted kind rather than only of the six seeded ones.
[ADR-0048](0048-the-kind-vocabulary.md) records where the vocabulary lives and why a near match warns rather than refuses, and [ADR-0049](0049-a-notes-kind-is-its-rank.md) records what a role does to ranking.

### 2026-08-15: `get` is fat, and bounded

This ADR says `get` is "deliberately fat", and that was implemented as unbounded.
One `get` against a real catalog returned 78,720 characters, all of it notes printed whole.

Fat was a decision about *what* `get` answers: the description, the attributes, the relations, the notes and the actions in one call rather than five.
That is untouched, and every one of those still arrives.
What changes is that the notes are bounded in bytes, and the ones past the bound arrive as their kind, their id and their opening line rather than whole ([ADR-0059](0059-what-a-list-may-not-leave-unsaid.md)).

The consequence this ADR already recorded, that "fat responses cost context on every call" and would be "annoying to reverse once agents depend on the shape", is the one being paid.
It is paid the smallest way available: nothing is dropped, and a note that did not fit is named and one call away.

The same record narrows this ADR's `search(query, kind?, limit?)`.
The `kind` argument was implemented as a pass over the page `limit` returned, which reported "nothing matches" for a kind whose matches sat past the window, and it now narrows the query itself.

### 2026-08-16: a note update leaves alone what it does not name, and `pinned` needs three states to say that

This ADR gives notes ids "so an agent can update rather than duplicate", and the update that shipped merges over what the file says: changing a body leaves the kind, the refs and the status as they were.

`pinned` was the one field that could not join that rule.
It arrived as a bare bool, and a bool has two values where the merge needs three, so an absent `pinned` and an explicit `pinned: false` were the same input.
**Replacing a note's body therefore unpinned it**, silently, on every update that did not restate the pin.

The cost is larger than one field.
Pinning is the operator saying a note is worth every future session ([ADR-0050](0050-what-the-context-budget-buys-first.md)), and it is what `dusk_context` leads with, so an agent tidying the wording of a gotcha removed it from every session that came after.
Nothing in the answer said so, because from the write path's side the note was written exactly as asked.

**The load-bearing rule is that a write changes only the fields it names**, and every input has to be able to express not naming one.
`pinned` is `boolean | null` in the tool schema and a `*bool` in the write path: absent leaves the note as the file has it, `true` pins, `false` unpins.
An explicit `null` reads as absent, the same way it does for `refs`.

This is the shape a sensitive plugin field already uses for "submitted empty means keep" ([ADR-0023](0023-plugin-configuration.md)), and it lands here for the same reason: a partial write that cannot say "I am not talking about this field" will eventually clear one.

Two alternatives were rejected.
A pair of `pin` and `unpin` booleans expresses three states in two fields, and makes a fourth, `pin: true, unpin: true`, that means nothing and has to be refused.
A `pinned` string enum of `on`, `off` and `unchanged` reads worse in a JSON schema than a nullable boolean and invents a vocabulary for a field that already had one.

The bad consequence to accept is that `null` and absent mean the same thing, so a caller that serialises every field of its struct and sends explicit nulls cannot unpin with a null and must send `false`.
That is the right way round: the ambiguous input is the one that changes nothing.
