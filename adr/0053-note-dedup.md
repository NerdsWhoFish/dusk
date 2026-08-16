# 53. A note already written down is that note, and one nearly written is a warning

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

[ADR-0010](0010-mcp-surface.md) gives notes three layers of deduplication, for a stated reason: an agent with no memory of a prior session cannot know an id, so ids alone do not stop it writing the same thing twice.
Ids were built. The other two were not.

The second layer was closer than it looked and worse than nothing.
[ADR-0031](0031-notes-are-files.md) makes the path the id, and a new note's path is its kind and the first eight characters of the body's content hash.
So an identical note already resolved to an identical path, a create presents no blob sha, and GitHub refused it with a 422 about a missing `sha`.
The mechanism that should have made duplication impossible produced an API error about something the caller had never heard of.

The third layer did not exist at all, which leaves the question this decision is really about: what should happen on a near-match rather than an exact one, and is a duplicate refused or reported.

[ADR-0033](0033-graph-integrity.md) answered the same shape of question for a ref that resolves to nothing: an unresolvable ref is the normal state of a catalog still being adopted, so the catalog reports it and keeps serving.
A note that overlaps another is the same kind of judgement Dusk cannot make.

## Considered Options

For an exact duplicate:

1. **Refuse it** as an error.
2. **Answer with the existing note's id**, and commit nothing.
3. **Overwrite it**, merging what the new call supplied over the note that is there.

For a near duplicate:

1. **Refuse it.**
2. **Write it and warn**, naming what it nearly repeats.
3. **Say nothing.**

And for measuring nearness:

1. **FTS5 ranking alone**, with a threshold on bm25.
2. **Word overlap**, over candidates FTS5 narrows down.
3. **Hashes only**, and no notion of nearness at all.

## Decision Outcome

Chosen: **answer with the existing id**, **write a near duplicate and warn**, and **word overlap over FTS5 candidates**.

### An identical note answers with the id, because that is what the caller needed

The knowledge is recorded. The call was to record it. Answering "it is at `.dusk/gotcha-1a2b3c4d.md`" succeeds at what the call was for, and hands over the one thing the caller could not have known.

Because the path is the hash, asking is a file read rather than a query.
That costs one extra GitHub read per new note, and it is answered **from git rather than from the index**, so it is right for a note written seconds ago that no reconcile has seen.
That is exactly when an agent is most likely to write it twice.

### Merging into the existing note was rejected by ADR-0009

[ADR-0009](0009-proof-tokens.md) lets a note create skip the proof token, on the grounds that a create cannot overwrite anything.
If an identical body merged its new refs into the note already there, a create would become an overwrite of a file the caller never read, and the reason for the exemption would be gone.

So a caller whose refs differ is told the id, and attaching it elsewhere is their next call, with a token from a read.

### A different note at the same path is refused

Eight characters name the file; the whole hash is what says the note is the same one.
They disagree only on a collision, and Dusk then refuses rather than overwriting prose somebody wrote.

### What ADR-0010 said, and what the file layout makes true

[ADR-0010](0010-mcp-surface.md) describes the exact layer as "an identical body **on the same entity**".
[ADR-0031](0031-notes-are-files.md) came later and put nothing about refs in the path, so "on the same entity" is not a question a path can answer.

The check is therefore body and kind: the same words filed as a `gotcha` and as a `todo` are two files, and the same words attached to two different entities are one.
Kind is in the path because kind changes what a note *is* ([ADR-0010](0010-mcp-surface.md) has it drive ranking and rendering), where refs only change what it hangs off.

### A near duplicate is written and named, following ADR-0033

Dusk cannot tell a duplicate from two notes that are legitimately about one subject, and refusing outright would make the catalog argue with the person filling it in.
So the note lands and the answer names what it nearly repeats, with enough of that note's text to recognise it.

### Nearness is FTS5 for recall and word overlap for the threshold

bm25 rank is relative to one query, so the same number means different things for different notes and no threshold can be written against it.
Word overlap can: the share of the combined vocabulary two notes have in common, counting each word once and ignoring words of two characters or fewer.
`0.4` sits between a lightly edited copy of a note and two different notes about one service, measured on real pairs.

FTS5 still earns its place, as recall: it narrows the catalog to fifty candidates for the cost of one query, so the overlap is counted over a handful of notes rather than all of them.

What this does not catch is worth being plain about.
There is no stemming and no stopword list, so "client" and "clients" are different words and "the" counts as one.
A genuine paraphrase sharing few words is missed.
**The exact layer is what prevents duplication; this one is a warning on top of it.**

### The warning is best effort

An index that cannot answer costs the warning, not the note.
Failing a write because the thing that was only ever advisory could not be computed would trade something the operator wanted for something they did not ask for.

## Consequences

### Good

- Writing the same note twice answers with an id instead of a 422 about a missing `sha`.
- That answer comes from git, so it is right during the window where the index has not caught up, which is the window where duplication actually happens.
- A near duplicate surfaces where the agent is already looking, in the answer to the write it just made.
- Nothing is refused for being similar, so a catalog somebody is still filling in is never argued with.
- "Notes like this" is now an ordinary index query, available to anything else that wants it.

### Bad

- **The exact check only sees the same kind**, because kind is in the path. The same words filed twice under different kinds are two files; the similarity warning catches it at a score of 1 and does not prevent it.
- One extra GitHub read per new note. Small against the two the write already makes, and it is per note rather than per entity, so it does not scale with how much a repository declares.
- **The similarity check reads the index, which lags the last reconcile.** Two notes written in one session do not warn about each other. The exact check has no such window, which is the half that matters most.
- The threshold is a judgement with failure modes on both sides: no stemming, no stopwords, and warnings that will sometimes fire on notes that are legitimately similar, which [ADR-0010](0010-mcp-surface.md) accepted in advance.
- Candidacy is decided on a note's longest thirty-two words, so a very long note is found by part of itself. Scoring still uses all of it.
- Being told the id is not the same as being able to use it: updating that note needs a proof token from a read, so the honest path is a read and then a write, which is two calls.

### Rejected because

- **Refusing an exact duplicate** was rejected because the knowledge is already recorded and the call was to record it. An error would make an agent try something else, and the something else is a second note.
- **Overwriting it** was rejected by [ADR-0009](0009-proof-tokens.md): it turns a create, which is exempt from the proof gate because it cannot overwrite, into exactly an overwrite.
- **Refusing a near duplicate** was rejected on [ADR-0033](0033-graph-integrity.md)'s reasoning. Where Dusk cannot tell a mistake from a legitimate case, it reports.
- **Saying nothing** was rejected because it is the state that lets duplicates accumulate quietly, which is the failure the three layers exist to prevent.
- **A bm25 threshold** was rejected because rank is not portable between queries, so the warning would fire inconsistently and nobody could say why.
- **Hashes only** was rejected because it catches nothing the exact layer does not. A note with one word changed hashes differently, and that is the most likely way a duplicate gets written.
