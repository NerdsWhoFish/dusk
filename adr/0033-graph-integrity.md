# 33. The catalog reports what is wrong with itself

Date: 2026-08-12

## Status

Accepted

## Context and Problem Statement

[ADR-0017](0017-engineering-policy.md) states the stakes plainly: Dusk's failure modes are quiet ones, and the only thing the product sells is that the catalog is correct.

Three of those quiet failures were shipped and known, each recorded in `docs/status.md` under Known gaps and each left to resolve silently.

**An entity declared in two repositories.** `Get` orders by repository and returns the first, so the answer is a coin toss presented as a fact. Nothing says the other declaration exists.

**A relation whose target nobody declares.** `neighbors` returns the edge and the reader follows it to nothing. The graph looks connected where it is not.

**A note attached to a ref nobody declares.** [ADR-0031](0031-notes-are-files.md) accepted this at write time, on the grounds that the target may legitimately live in a repository Dusk cannot see. The consequence is that a one-character typo produces a note that is findable by search and will never appear on the thing it is about.

All three share a shape: the catalog has enough information to know something is wrong and says nothing.
An operator only discovers them by noticing an answer is wrong, which is exactly the discovery path a catalog exists to remove.

## Considered Options

1. **Reject at write time.** Refuse a declaration that duplicates one, a relation to an unknown ref, a note ref that does not resolve.
2. **Fail the reconcile.** Treat an unsound graph as a broken repository and refuse to index it.
3. **Report, and keep serving.** Index everything, and expose what is wrong as a first-class read.
4. **Leave it.** Document the gaps and rely on the operator noticing.

## Decision Outcome

Chosen: **option 3**.

The catalog answers a new question, "what is wrong with you", through `index.Integrity`, `GET /api/integrity`, and an `integrity` MCP tool.

### Reporting rather than rejecting, because the catalog is partial by design

Options 1 and 2 both assume Dusk can tell a mistake from a legitimate absence, and it cannot.

A relation to `host:home/nas` is correct whether or not the repository declaring that host is one Dusk was installed on.
[ADR-0004](0004-dusk-md-convention.md) makes participation opt-in per repository and [ADR-0030](0030-account-allowlist.md) narrows it further, so **an unresolvable ref is the normal state of a catalog that is still being adopted**.
Rejecting it would make the correct incremental path, describe one repository today and another next month, fail.

Failing the reconcile is worse still: it takes a repository out of the catalog for a problem that may live in a different repository entirely, which is a failure mode [ADR-0011](0011-ingester-scheduling.md) already rules out for ingesters and should not reintroduce here.

### A problem names every place involved

A report that says "something is declared twice" and stops has moved the search rather than ended it.
Each problem carries the repository and path of everything implicated, so the first action is opening a file rather than grepping for one.

### Soundness is a read, not a log line

Logging these at reconcile time would put them where nobody looks, scattered across the sweep of whichever repository happened to trip them.
Making it a read means an agent can ask before trusting an answer, and the UI can show it without Dusk deciding when it matters.

## Consequences

### Good

- The three known silent failures are now loud, on demand, without changing what the catalog will accept.
- An agent that gets a surprising answer has somewhere to look. "Confidently wrong" and "correct" were previously indistinguishable from outside.
- Adoption stays incremental. Describing one repository at a time still works, and the unresolved refs that produces are reported as information rather than treated as errors.
- One call covers every class, so "is my catalog sound" is one question.

### Bad

- **A partially adopted catalog reports a lot of dangling relations, and most of them are fine.** This is the real cost: the signal is noisiest exactly when a new user is most likely to look at it, and nothing here distinguishes "typo" from "not adopted yet".
- Integrity is computed on demand and scans the graph, so it costs more than the reads beside it. Fine at a homelab's scale, and the first thing to feel a large catalog.
- Duplicate detection reports the conflict but nothing resolves it. `Get` still returns whichever sorts first, so the catalog remains arbitrary until a human picks.
- Nothing yet checks that a relation's `type` is one of a known set, or that an entity's kind is, because [ADR-0007](0007-entity-schema.md) has no vocabulary to check against.

### Rejected because

- **Option 1** was rejected because it cannot distinguish a typo from a repository Dusk has not been shown, and would therefore block the incremental adoption the whole convention is built for.
- **Option 2** was rejected because it punishes a repository for a problem that may not be its own, and because [ADR-0011](0011-ingester-scheduling.md)'s rule that a failure must never look like a deletion applies with equal force here.
- **Option 4** was rejected because it had already been chosen once by default, and produced three documented gaps that no user would ever find on their own.

## Amendments

### 2026-08-12: a duplicate needs two sources of equal standing

Duplicate detection grouped every row sharing a ref, which was written before [ADR-0034](0034-ingesters-in-tree-first.md) put observations in the same table.
The result was that declaring an entity at the ref an ingester already reports, the exact thing an operator should do, was reported as a problem.

It is not one.
`Get` orders on `observed` before `repository`, so a human declaration beats an ingester's observation deterministically, and the pair is agreement rather than a coin toss.
The grouping now includes `observed`, which leaves the two real cases: two repositories declaring one ref, and two ingesters observing one ref.
Neither has a tiebreak beyond sort order.

This also corrects the third Bad consequence above.
`Get` is no longer arbitrary between a declaration and an observation; it is arbitrary only among sources that rank equally, which is what is now reported.
