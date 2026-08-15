# 9. Proof tokens gate every write

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Agents are the primary writers, per the thesis in [DESIGN.md](../DESIGN.md).
Three failure modes follow from that, and they look separate at first.

An agent with no memory of a previous session writes a note that already exists, and the catalog accretes near-duplicates until nobody trusts it.

Two agents update the same entity from stale reads, and one silently clobbers the other.

An agent invents `kind: srvice` because it never checked what kinds exist, and the taxonomy fragments in exactly the way [ADR-0007](0007-entity-schema.md) names as its main risk.

Each of these has an obvious individual fix: content dedup, optimistic concurrency, a vocabulary gate.
Three mechanisms, three sets of edge cases.

They share a root cause.
In every case the agent wrote without first looking.

## Considered Options

1. **Three independent mechanisms**: content-hash dedup, version-based optimistic concurrency, and a separate vocabulary check.
2. **A universal invariant**: no write is accepted without evidence that the writer read current state first.

## Decision Outcome

Chosen: **option 2**.

**Every agent write requires a proof token, issued by a read.**

### Humans never present tokens

The gate is agent-facing, and deliberately does not apply to the UI.

Every failure it prevents comes from writing without looking, and a human editing in the UI cannot do that: the read is the screen in front of them.
They searched, they opened an entity, they clicked edit on a specific thing.
Requiring a token there would enforce the letter of this decision while its entire purpose is already satisfied, and it would put protocol ceremony in front of a person for no gain.

Concurrency is still handled on the human path, just not by tokens.
Per-session branches in [ADR-0010](0010-mcp-surface.md) mean two people editing the same entity collide at merge, which is where a collision belongs and where git already knows what to do.

A token is issued by any read operation (`get`, `search`, `neighbors`, `getKinds`) and carries the set of `(ref, version)` pairs that read returned, plus an issue time.

A write is accepted only if the ref being written is covered by the presented token and has not changed since.

### Creation is gated by a search that did not find it

To create something that does not exist, the agent presents a token from a search whose results did **not** contain it.

An agent therefore cannot create a duplicate without having first looked for one.

### Tokens cover sets, not single objects

Because a token carries everything its originating read returned, one `search` authorizes many subsequent writes.
The cost of the invariant amortizes across a working session rather than doubling every call.

### Invalidation is by change, then by time

A token is invalidated when any ref in its set changes.
A TTL exists only as a backstop.

Time-based expiry alone either forces agents to re-read constantly or proves nothing about currency.

### Failures must be actionable

A rejected write returns the exact call that would fix it:

```text
E_PROOF_REQUIRED: call get("service:home/scrypted") first
E_PROOF_STALE: service:home/scrypted changed; call get("service:home/scrypted") again
```

This is not a convenience.
Read-before-write is an unusual contract, and an agent that cannot discover how to satisfy it will flail.
An unactionable error makes the invariant hostile instead of teachable.

## Consequences

### Good

- One mechanism replaces three, with one set of edge cases instead of three.
- Duplicate creation becomes structurally difficult rather than discouraged, because creating requires having searched.
- Concurrency is handled without locking. A stale write is rejected with an instruction rather than silently winning.
- Vocabulary fragmentation is caught by the same gate that catches everything else, since `mintKind` is a write like any other.
- Set-scoped tokens keep the round-trip cost proportional to sessions rather than to writes.

### Bad

- Every write path must validate tokens, and every read path must issue them. That is pervasive plumbing, and it is difficult to add later.
- Token state must be stored and expired somewhere, which is a new lifecycle to operate.
- An agent that reads and then thinks for a long time will find its token stale and must re-read. That is correct behaviour and will still feel like friction.
- **The token proves retrieval, not comprehension.** An agent can call `get` and ignore the result entirely. This is a guardrail against carelessness, which is the actual failure mode, and it must not be described as a security control.

### Rejected because

- Three independent mechanisms were rejected as more surface for less coverage. They address the symptoms separately while leaving the shared cause unaddressed, and each carries its own configuration, failure modes, and documentation.

## Amendments

Amendment policy: [ADR-0028](0028-amending-adrs.md).

### 2026-08-15: creation is gated by the read that could have witnessed the absence

This ADR says creation is gated by "a search that did not find it", and the implementation required exactly `search`.

That is right for an entity and impossible for anything else.
`search` reads the catalog, and the two things Dusk writes that are not entities live at fixed paths in the config repository: `.dusk/home.md` and `.dusk/kinds.md`.
No search can name a file, so a repository that had never declared a homepage could not declare one at all: `page` issued its token, `SetHome` took the create path, and the answer told the agent to call `search(".dusk/home.md")`, which cannot answer the question being asked.

The rule generalises rather than loosens.
**A create is authorized by a read that could have returned the thing and did not.**
For an entity that is still `search`, because nothing else enumerates.
For a singleton it is that singleton's own read, which is a stronger witness than a search would be: `page` looks at exactly the file being created, where a search infers absence from a query.

Nothing else changes. An entity create still refuses a `get` token, on this ADR's own reasoning that resolving one name cannot witness an absence.

### 2026-08-15: the fix a rejection names is supplied by the caller

This ADR requires a rejected write to return "the exact call that would fix it", and shows two examples, both of them `get` on an entity ref.

The implementation read that as a rule it could derive, and phrased every rejection as `get(<ref>)`.
That is true only for entities.
A note's ref is its file path, which `get` refuses outright; `kinds` and `page` take no ref at all.
So every rejection outside the entity path named a call that does not work, which is worse than an unactionable error: the agent follows it, fails a second time, and concludes the tool is broken.

The call is now a second fact the write path supplies alongside the ref, because nothing turns one into the other.
`proof.Subject` carries both, and each rejection renders the read that subject names.

It also made a call exist that did not: `note(id: …)` on its own now reads that one note, since naming a call an agent cannot make would have been the same defect again.
