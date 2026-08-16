# 62. `relate` declares an outbound edge, and checks the target's shape but not its existence

Date: 2026-08-16

## Status

Accepted. Implements the `relate` tool [ADR-0010](0010-mcp-surface.md) named and never built.

## Context and Problem Statement

[ADR-0010](0010-mcp-surface.md) listed `relate(from, to, type)` as one of the write tools and said nothing else about it.
Everything that decides its shape was settled afterwards, by decisions that were not about relating at all, and between them they leave much less room than that signature suggests.

[ADR-0026](0026-dusk-md-schema.md) puts a relation in the frontmatter of the file that declares the entity it points **from**, and makes the `from` side derived rather than authored.
It calls that load-bearing: a repository can never assert a fact about an entity it does not own, so catalog review is code review of the repository making the claim.
A tool that could write an edge in either direction would delete that property.

[ADR-0033](0033-graph-integrity.md) refuses to check that a relation's target resolves, because an unresolvable ref is the normal state of a catalog still being adopted, and rejecting one would make the correct incremental path fail.

[ADR-0009](0009-proof-tokens.md) requires a proof token from a read that could witness the current state, and its second amendment requires the rejection to name the call that recovers.

What is left open is small and worth being deliberate about.
Which read authorizes the write.
What happens when the file already declares that exact edge.
And what, if anything, is checked about the two refs, given that one of them is explicitly not to be checked.

## Considered Options

1. **A `relations` argument on `declare`**, so the tool count does not grow.
2. **A separate `relate` tool** that adds one edge.
3. **A separate `relate` tool that both adds and withdraws**, since nothing else can remove an edge.

## Decision Outcome

Chosen: **option 2**.

`relate(from, to, type, proof)` appends one edge to the frontmatter of the file that declares `from`, and commits it like any other write.

### A separate verb, because a list has no merge semantics

Option 1 is the one that had to be argued rather than dismissed, since [ADR-0010](0010-mcp-surface.md) makes the size of the tool list a product constraint and a ninth tool is a real cost.

It loses on what `relations` would have to mean inside `declare`.
Every field `declare` takes is merged, and an absent one is left alone, which is what stops setting an attribute from blanking a description.
A list cannot join that rule.
`relations: [...]` would have to mean either *replace all of them*, in which case a `declare` that changes a title and mentions no relations silently deletes every edge the entity has, or *append*, in which case one field of one tool appends while every other field overwrites and nothing in the call says which.

The first is precisely the failure this same change fixed one level up, where `pinned` could not tell "leave it alone" from "turn it off" and an edit unpinned the note ([ADR-0010](0010-mcp-surface.md)'s 2026-08-16 amendment).
The second is worse, because it is invisible: an agent reading the tool description has no way to learn that one argument behaves unlike the rest.

A verb whose name is the operation has neither problem.
`relate` adds an edge, which is what "relate" means, and there is no field whose semantics have to be explained.

### The token is a read of the entity the edge points from, and any read will do

A relation is a change to the source entity's own declaration, so it routes through `Locate` and is authorized by `proof.Entity(from)` exactly as an update to that entity is.
A rejection therefore names `get("<from>")`, which is the read of the thing being written.

The token's **origin** is deliberately not constrained.
The close alternative was to require `get` or `neighbors`, on the reasoning that only those two print the edges an entity already declares, so an agent holding a `search` token can add a second `runs_on` without ever having seen the first.

It loses for two reasons.
The harm it guards against does not exist, because an edge the file already declares is answered rather than written, below.
And [ADR-0009](0009-proof-tokens.md) says in its own consequences that the token proves retrieval and not comprehension, and that set-scoped tokens exist so the cost amortizes across a session; requiring a particular read here would enforce a stronger contract than the invariant actually makes, on the one write with the least to justify it.
`proof.Subject` also carries one `Read` rather than a set, and teaching it "either of these two" would be new machinery for a guard with nothing behind it.

The version check still does the work that matters.
A token recording the entity at a version other than the one on disk is stale whatever read issued it, so an edge can never be appended to a file that moved after it was read.

### An edge the file already declares is answered, never written twice

A second copy of an edge says nothing the first did not, and a diff adding a duplicate line cannot explain itself.
`relate` reads the file, finds the same `type` and `to`, and answers with where it is already declared, committing nothing.

That is the shape an identical note already has ([ADR-0053](0053-note-dedup.md)): writing something already written down is a mistake worth naming and not one worth refusing.

### The target's shape is checked; whether it resolves is not

[ADR-0033](0033-graph-integrity.md)'s rule is about **resolution**, and it is untouched: a `to` nothing declares is committed, because the thing at the other end may live in a repository Dusk was never shown.

**Syntax is a different question and the opposite answer.**
`duskmd` refuses a `to` that is not a ref of the form `kind:namespace/name`, so committing one produces a file that no longer parses, and a file that no longer parses takes its entity out of the catalog on the next reconcile.
A write that reports success and then deletes the thing it wrote to is the quiet failure this product exists to remove.

So `relate` parses both refs with the same `conformance.ParseRef` the reader uses, and refuses before committing.
Checking the shape is what makes tolerating the unresolved safe rather than fatal.

### An unresolvable target is written and named

The answer says when nothing in the catalog declares the target.

A typo and a repository Dusk has not been shown produce byte-identical files, so `integrity` cannot separate them and neither can Dusk ([ADR-0033](0033-graph-integrity.md) records that as a known cost).
The caller that just typed the ref is the only participant that can, and it is the one holding the answer.
This is the same posture the near-duplicate note warning and the near-match kind warning already take: write, then say what was nearly meant.

## Consequences

### Good

- The only direction expressible is the one [ADR-0026](0026-dusk-md-schema.md) permits, so the trust boundary is inherited from the tool's signature rather than enforced by a check somebody could forget.
- An agent that has read an entity can connect it without a second read, because the token it already holds covers the write.
- A wrong ref fails as a refused call rather than as a repository that stops parsing, which is the difference between an error and a deletion.
- The duplicate case is decided once, in the write path, so the surface has nothing to explain and the answer is the same over MCP as it would be anywhere else.

### Bad

- **Nothing withdraws an edge.** A relation to something decommissioned is fixed by hand, in the repository that declares it, and the maintenance queue [ADR-0045](0045-drift-is-a-maintenance-queue.md) builds is therefore one an agent can read and not act on. This is the sharpest cost of the decision and it is deferred rather than missed, recorded under Next in `docs/status.md`.
- **A relation's `attributes` cannot be authored over MCP.** The format carries them and `relate` does not, so an edge that wants one is written by hand. Adding it later means deciding whether a second `relate` on an existing edge merges attributes or replaces them, which is the merge question this ADR avoided by refusing duplicates, arriving from the other side.
- A ninth tool is a real cost against [ADR-0010](0010-mcp-surface.md)'s own constraint, and the argument above is why it is paid rather than a reason it is free.
- An agent can still declare an edge that contradicts one already there, such as a service running on two hosts, because Dusk has no vocabulary of relation types and [ADR-0033](0033-graph-integrity.md) has nothing to check cardinality against.
- The unresolvable-target warning costs one index read on every successful relate. It is the same read `get` makes and it is paid only after the commit, so a slow catalog delays an answer rather than a write.

### Rejected because

- **A `relations` argument on `declare`** was rejected because a list has no merge semantics: replacing means a `declare` that does not mention relations deletes them, and appending means one argument behaves unlike every other one in the same call, with nothing in the description saying so.
- **A `relate` that also withdraws** was rejected as underspecified rather than unwanted. Removal has to say *which* edge, and the candidate spellings differ in what they do to an entity with several edges to one target. [ADR-0010](0010-mcp-surface.md) also names an `unset` on `declare` that has never been built for any field, and withdrawing an edge is the same idea; deciding both together is better than inventing a second spelling of it here.
