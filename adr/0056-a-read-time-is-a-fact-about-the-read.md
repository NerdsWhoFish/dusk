# 56. A read time is a fact about the read, and it is stored with what was read

Date: 2026-08-15

## Status

Accepted

## Context and Problem Statement

`changes` exists to tell a stale answer from a missing one.
Its own description says so, and it is the only tool that could catch the catalog being confidently wrong.

It returned commit hashes and counts and no time at all.
`SyncStatus` carried no time field and neither did a plugin's health report, so the tool whose purpose is answering "is this current" could not answer it.

That matters more than it sounds, because every one of Dusk's normal failure modes is quiet and slow.
The poll floor is a day ([ADR-0006](0006-reconcile-triggering.md)), webhook deliveries are lost in ordinary operation, and a failing ingester deliberately keeps serving what it last observed ([ADR-0011](0011-ingester-scheduling.md)).
Each of those produces a catalog that answers confidently with something old, and none of them produces an error anybody sees.

ADR-0011 already decided where staleness comes from: the `observed_at` [ADR-0007](0007-entity-schema.md) puts on every message, "so no separate freshness tracking is required".
Nothing had ever surfaced it.

The hard question is not where to get a time.
It is what a read time is allowed to mean when the thing holding it is disposable.
[ADR-0008](0008-storage.md) makes the index rebuildable from git at any time, and a freshness signal that resets to "just now" whenever Dusk restarts is worse than no signal at all, because it is wrong in the reassuring direction.

## Considered Options

For where the read time lives:

1. **Remember it in the process**, on the controller and the scheduler, where the reads already happen.
2. **Write a durable record beside the index**, a table or a file of "last read" rows Dusk maintains itself.
3. **Derive it from the `observed_at` already stored with the content.**

For what "read" means when a commit has not moved:

1. Only a read that downloaded and parsed a tree counts.
2. Confirming the ref still points at the indexed commit counts too.

For rendering:

1. An absolute timestamp.
2. A relative phrase.
3. Both, relative first.

## Decision Outcome

### The time is derived from `observed_at`, and it is a fact about the read

Chosen: **option 3**, `index.LastRead`, one query grouping the default view by its repository slot.
An ingester's scope occupies that slot, so an observation and a repository are dated by the same mechanism rather than by two.

This is durable because it is stored with the content it describes.
A restart does not touch it.

The part worth stating plainly, because it looks like the trap and is not:
**after an index rebuild the time moves to now, and that is true rather than convenient.**
A read time is a fact about the read, not about the content.
Rebuilding the index re-reads every repository from git, so "read just now" is exactly what happened, and the content being a year old is a separate fact the commit already carries.

The failure this avoids is the opposite one: an in-memory time goes to zero on restart, and a catalog full of week-old entities then reports that nothing has ever been read.

### The controller keeps the fresher answer, and the index is the floor

Confirming a commit has not moved reads nothing, so it leaves no trace in the index, but it does establish that the catalog matches git as of that moment.
That is the more useful answer and only the running process has it, so `controller.Status.At` is kept and preferred, with the durable time used when memory has none.
The durable time is a floor: it is only ever older, never fresher, so falling back to it can make an answer look staler than it is and can never make it look fresher.

`At` and `Attempted` are now separate.
Recording a failure against `At` dated the catalog by when reading it broke, which made "failed four hours ago, last read a week ago" render identically to "read four hours ago".

### The retry schedule is part of the answer

The ingest scheduler already counted consecutive failures and computed the next attempt, and reported neither.
Both now travel on `ingest.Result` and reach `plugin.Health`, and the controller reports when the poll floor next runs.
A reader asking whether an answer is current is partly asking when it will be corrected, and with a day's floor that is not a detail.

This is adjacent to a known gap and does not close it: a sweep that exhausts the GitHub rate limit is not waited out, it gives up until the next sweep.
`changes` now says when that sweep is, which makes the gap legible rather than fixed.

### Both halves of every time, relative first

Chosen: **option 3**, `4 hours ago (2026-08-15T08:00:00Z)`.

The reader is usually an agent, which is the whole reason to give the relative half.
An agent has no dependable sense of now: a session outlives whatever its context said the date was, so asking it to subtract two timestamps is asking it to guess.
Dusk knows what time it is, so it does the arithmetic and the relative phrase is the number that actually answers "is this stale".

The absolute half is what correlates with anything else, a commit, a deploy, another tool's output, and it is the only form that survives being quoted into a note or read back later in the session.
A relative phrase rots the moment it is copied.

The phrasing is deterministic rather than approximate, in seconds, minutes, hours or days with no "about", because two of these get compared and a reader should not have to work out what a rounding covered.

## Consequences

### Good

- `changes` can answer the question it exists for. A repository that failed four hours ago while last succeeding a week ago no longer renders the same as one read a minute ago.
- The freshness signal survives a restart, which is when it is most needed and was previously absent, because a restart is also when the sweep has not finished.
- Nothing new is recorded. The read time, the failure count and the next attempt all existed already; this change carries them to the surface, so there is no second record to keep in step with the first.
- One mechanism dates a repository and an ingester, so a plugin that has been failing since Tuesday says how old what it is serving is, which is the case ADR-0011 creates deliberately and nothing surfaced.
- ADR-0011's claim that `observed_at` makes separate freshness tracking unnecessary is now true rather than aspirational.

### Bad

- Reading a repository whose commit has not moved still rewrites its `observed_at`, so an index rebuild moves every read time to now. Honest, and it will read as suspicious to somebody who expects the number to describe the content.
- `changes` now costs one more query. It is an aggregate over a table already fully scanned by other reads, and `changes` is not a hot path, but it is not free.
- A repository the index knows about and the controller has no status for is still absent from the answer entirely, so the durable floor only rescues entries that exist. Right after a restart, before the first sweep finishes, `changes` can still under-report which repositories there are.
- A plugin whose process is dead reports no health at all, so its observations have no age on them. Its line says it is not running, which is the more important half, but the two states are surfaced by different code paths and only one of them is dated.
- Two times per line makes `changes` longer, on a surface where output length is a real cost.

### Rejected because

- **Remembering it in the process** was rejected on the restart case, which is the exact moment the answer is worth most. It reports "never read" for a catalog full of content, and the fix for that, defaulting to now, is the same bug pointed the reassuring way.
- **A durable record beside the index** was rejected as a second source of truth for something already recorded. It would need writing on every read, keeping in step with what it describes, and deciding what happens when the two disagree, to hold a fact `observed_at` already holds correctly. [ADR-0015](0015-plugin-actions-and-events.md) keeps events out of SQLite because they cannot be rebuilt from git; a read time is not in that category, because re-reading is what produces it.
- **Counting only a downloaded tree as a read** was rejected because a repository nobody changes would age forever while Dusk confirmed it hourly. Confirmation is a stronger statement than a re-read, not a weaker one: the commit is provably the indexed commit.
- **An absolute timestamp alone** was rejected because it makes the reader do arithmetic it cannot reliably do.
- **A relative phrase alone** was rejected because it is ambiguous the moment it leaves the answer, and correlating with a commit or a log line is exactly what somebody does next when the answer is bad.
