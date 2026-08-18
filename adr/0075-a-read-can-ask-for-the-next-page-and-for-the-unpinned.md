# 75. A read can ask for the next page, and for the unpinned

Date: 2026-08-18

## Status

Accepted. Extends [ADR-0059](0059-what-a-list-may-not-leave-unsaid.md).

## Context and Problem Statement

[ADR-0059](0059-what-a-list-may-not-leave-unsaid.md) made every list say what it was not showing, and told the caller what to do about it:

> an agent told "10 of 35" knows to raise the limit or narrow the filter

Both halves of that advice turned out to be reachable dead ends.

**Raising the limit cannot get past a caller's response cap.**
`limit` is the size of the page, and the page always starts at the first row, so asking for the rest means asking for what was already read *plus* the rest.
An agent whose client rejects a result over some size can never receive rows past that point, no matter what it passes.
The concrete case: 117 notes, roughly 64KB of markdown, a client that refuses the result.
Narrowing by `kind` happened to work only because the kinds were small enough; a catalog with 200 runbooks has no filter that helps and no way to page.

**And `pinned` was accepted as a read filter and silently ignored.**
`noteInput` has taken a `pinned` field since notes could be pinned, described as what pins and unpins one.
Reading with it is a natural thing to try, the tool accepts it without complaint, and `readNotes` never copied it into the filter, so it did nothing.

That is worse than an unsupported argument, because of the ordering.
`Notes` sorts `pinned DESC` first, so an unfiltered page is *led* by the pinned notes.
A caller asking for the pinned and receiving a page whose visible rows are exactly the pinned ones has every reason to believe the filter worked.
Ask for a larger page and the pinned ones still come first, followed by everything else, and the honest reading of that is "everything is pinned".

That is not hypothetical: it is how an operator concluded all 117 of their notes were pinned when three were, and it sent them to unpin 114 notes that were never pinned.
A filter that is dropped rather than refused does not merely fail to narrow. Combined with an ordering that mimics it, it manufactures a false answer and supplies the evidence for it.

## Considered Options

For paging:

1. **`offset`**, the row count to skip.
2. **An opaque cursor** carrying the sort position.
3. **Nothing. Narrow harder**, which is the current state.

For the dropped filter:

1. **Honour it, as a tri-state**: unset is every note, true only pinned, false only unpinned.
2. **Honour it as a plain bool**, where true narrows and false means "do not narrow".
3. **Reject it on a read**, so the caller learns it is not a filter.

## Decision Outcome

Chosen: **`offset`**, and **the tri-state filter**.

`note` and `search` both take `offset`. Their headings name the exact next one rather than advising a larger limit:

```text
1-5 of 35 note(s), newest first. Ask again with `offset` 5 for the next page, or narrow by `kind`, `status`, `pinned` or `ref`.
```

This is [ADR-0059](0059-what-a-list-may-not-leave-unsaid.md)'s rule unchanged and its advice corrected.
A list still says what it holds and what it is not showing; what it now names is a step the caller can actually take.
The last page names no offset, because pointing past the end is its own small lie.

`search` pays nothing for this: its total is a `COUNT(*) OVER ()` across every ranked row, computed before `LIMIT`, so it stays the size of the whole result as the offset moves.

**A cursor was rejected**, though it is the more correct primitive.
It is stable against concurrent writes, which offset is not: a note written between two pages shifts every row after it and can hide one.
That race is real and was accepted, because the catalog is reconciled from git commits rather than written continuously, an agent pages through a result in seconds, and the failure is a missed row rather than a wrong answer.
A cursor is also opaque, and an agent that can compute its own next offset from the numbers already in front of it needs no round trip to discover it.

**`Pinned` becomes `*bool` in the filter**, so it can express three questions rather than two.
The plain-bool version was rejected for exactly the reason this ADR exists: "only the unpinned" is the question an audit of what pinning is carrying has to ask, and a plain bool cannot ask it. Had it been askable, the false conclusion above would have been one call to disprove.

**Rejecting it on a read** was rejected as a smaller version of the fix.
It removes the false answer and leaves the question unanswerable, and the field is already there, already documented, and already means the obvious thing.

The write path is untouched: with an `id`, `pinned` still pins and unpins. `kind` and `status` already carry that same dual meaning, so `pinned` now matches them rather than being the one field that reads differently from how it is written.

## Consequences

### Good

- A result larger than a caller's response cap is now reachable in full, which it was not by any argument the tool accepted.
- The dead advice is gone. Every cut list names a step that works.
- "What is actually pinned here" is one call, and answering it wrongly now takes effort rather than being the default.
- One predicate builder still serves the read and the count, so a filtered page and its total cannot disagree ([ADR-0059](0059-what-a-list-may-not-leave-unsaid.md)).

### Bad

- **Offset is not stable against concurrent writes.** A note added between two pages shifts the rows after it, and a caller paging through a changing catalog can miss one. This is accepted rather than solved.
- Deep offsets make SQLite walk and discard the rows before them, so paging far into a large result costs more per page than the first.
- Three fields now describe one page (`limit`, `offset`, `total`) where there were two, and the "N-M of T" heading is a third place the same arithmetic appears.
- `*bool` in a filter struct is a nil check every caller has to get right, and forgetting one silently means "every note" rather than failing.

### Rejected because

- **An opaque cursor** was rejected as correctness the caller pays for in opacity, against a race that is bounded by how this catalog is written and whose worst outcome is a missed row.
- **Narrowing harder** was rejected because no filter exists for "the rest of one kind", so the dead end stays a dead end.
- **A plain bool** was rejected because it cannot ask for the unpinned, which is the question this whole failure needed.
- **Refusing `pinned` on a read** was rejected because it removes a wrong answer without supplying the right one.
