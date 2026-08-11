# 28. ADRs are amended in place, and retired rather than deleted

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

The standing rule has been that history is never rewritten: a reversed decision gets a new ADR that supersedes the old one, and the old one stays.
That rule protects the thing ADRs exist for.
The value is the rejected alternatives and the reasoning, and reasoning that can be quietly edited later is worth nothing, because a reader cannot tell whether they are looking at what was decided or at what somebody later wished had been decided.

Taken literally, though, the rule has no answer for a document that has become *inaccurate* without its decision changing.

[ADR-0027](0027-design-target.md) produced exactly that case.
Three ADRs described the intended user as a platform team inside a company.
That was never a decision any of them made; it was an assumption absorbed from Backstage and repeated in passing.
Once the design target was settled, those sentences were simply wrong.

Superseding is the wrong instrument for it.
Superseding [ADR-0003](0003-license.md) would replace a licensing decision that nobody wants to change, purely to fix a noun in its context statement.
Doing nothing is also wrong, and worse: three ADRs would go on asserting an audience the project has formally rejected, and a reader has no way to know which sentences to disbelieve.

So the question is not whether the record should be protected, but which part of it is the record.

There is a second gap, smaller and already visible in the documentation.
Both [`CLAUDE.md`](../CLAUDE.md) and [`adr/README.md`](README.md) instruct that "adding, superseding, or **removing** an ADR" updates the index in the same change.
Removing one is therefore already sanctioned in passing, and nowhere defined, which is the worst combination available: a destructive operation with no stated rules.

It also names a real state that superseding does not cover.
A decision can stop applying without anything replacing it, because the thing it governed was cut.
Such an ADR is not superseded, and leaving it marked `Accepted` presents a dead rule as live guidance.

## Considered Options

1. **Strict immutability.** Never edit an ADR after acceptance. Inaccuracies are fixed only by superseding.
2. **A separate errata document** listing corrections to ADRs, leaving the ADRs untouched.
3. **Amendment in place, with a dated record** of what changed and why.

## Decision Outcome

Chosen: **option 3**.

An ADR may be amended in place, and every amendment is recorded in a dated `## Amendments` section at the bottom of the file saying what changed and why.
Its `## Status` is marked so the amendment is visible to somebody who reads no further.

That includes substance.
Reasoning improves, an option turns out to have been mischaracterised, a consequence lands differently than predicted.
A document that goes on asserting reasoning nobody holds any more is not a more honest record than one corrected in the open; it is just a wrong one that is harder to fix.

**The boundary is scale, not category.**
Any part of a document may be corrected.
What may not happen in place is a wholesale rewrite of its major sections, because a file rewritten that far is no longer the same document, and keeping its number claims a continuity that is not there.
The practical test is whether it still argues the same thing it argued before.

Changing the decision is the clearest case, since that rewrites the section the whole file exists for.
Editing [ADR-0008](0008-storage.md) to say PostgreSQL because the storage engine changed would not be an amendment, it would be falsifying the record, and superseding exists for precisely that.

### An ADR is retired, never deleted

A decision that no longer governs anything is marked `Retired`, with a dated entry saying what happened.
It keeps its number, stays in the index marked as retired, and its file is never removed.

Retirement covers two cases that look different and read the same:

- What the decision governed was cut, so there is nothing left for it to rule on.
- The decision was withdrawn without a replacement. We do not do this any more, and have not decided what to do instead.

They are one status rather than two because the distinction is in the cause, not the consequence.
Either way a reader needs to know the rule is dead, and the dated entry is where the difference gets explained.

This is distinct from superseding, which means something *replaced* the decision and tells a reader where to go next.
Retirement has nowhere to send them, and saying so is the point.

**Deletion is never correct.**
The value of an ADR is concentrated in its rejected alternatives, so a deleted file takes with it precisely the part that would have stopped the same argument being had again.
A retired decision is still the reason nobody should re-propose what it rejected.

**Disclosure is the safeguard, not immutability.**
The dated entry states what changed and why, so a reader can weigh the correction for themselves.
That protection applies uniformly, which is why this ADR does not try to police a boundary between wording and substance.
Such a boundary is a judgement call, judgement calls erode, and the disclosure requirement makes it unnecessary.

## Consequences

### Good

- Documents that have become wrong can be made right without pretending a settled decision was reopened, which is what superseding would have implied.
- The correction and the reasoning stay in the file a reader is already looking at, rather than in an errata document nobody opens.
- The dated entries make amendment visible and cheap to audit. A file with no `## Amendments` section is verbatim as accepted, which is a stronger guarantee than the old rule actually delivered, since the old rule had no mechanism to prove a file had never been touched.
- It gives ADR-0027's corrections somewhere to go that is proportionate to what they are.
- There is no category boundary to police, so no argument about whether a given fix counts as wording or substance. Reasoning that turns out to be wrong gets corrected instead of being left standing because a rule made fixing it expensive.
- "Removing an ADR" stops being a destructive operation the documentation sanctioned without ever defining, and the index stops implying deletion is on the table.
- A decision that has stopped applying is visibly dead rather than quietly stale, which matters most for the reader who has no way to know the feature it governed was cut.

### Bad

- Reasoning can be improved after the fact, which makes past thinking look better than it was. An ADR is partly a record of how well a decision was reasoned at the time, and correcting an argument erodes that, in a way the dated entry discloses but does not undo.
- "Wholesale rewrite" is a judgement call as well, just a coarser one than the alternative. The realistic failure is arriving there one correction at a time, where no single entry looks wrong and the file has quietly stopped arguing what it used to.
- Amendment is easier than superseding, so it will be reached for when superseding is correct. The cost of getting that wrong is a decision quietly changing without a record, which is the exact failure the original rule existed to prevent.
- Git history already recorded every edit, so this adds a second, hand-maintained record that can disagree with the first. It is justified by ADRs being read as rendered documents rather than as a commit log, but it is duplication.
- An ADR amended many times accumulates a tail of entries that is longer than the decision, which is a signal it should have been superseded and will not always be read as one.
- The index only ever grows. Retired entries stay in it, so a newcomer skims past decisions that no longer apply to reach the ones that do.
- Retiring is less work than superseding, so a decision that genuinely was replaced may get retired instead, losing the forward pointer that is the more useful half of superseding.

### Rejected because

- **Option 1** was rejected because it protects the wrong thing. It treats every sentence in an ADR as part of the record, including sentences that were never decided, and its only remedy is to reopen decisions that are not in question. A rule whose correct application produces an obviously wrong outcome gets ignored rather than followed.
- **Option 2** was rejected because it splits the record. A reader of `0003-license.md` would have to know an errata file exists and think to check it, and corrections that live away from the thing they correct do not get read. It also has all of this option's judgement problems while adding a second place to look.
