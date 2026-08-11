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

**Every write in Dusk requires a proof token, issued by a read.**

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
