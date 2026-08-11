# 28. ADRs may be amended in place, with a dated record

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

## Considered Options

1. **Strict immutability.** Never edit an ADR after acceptance. Inaccuracies are fixed only by superseding.
2. **A separate errata document** listing corrections to ADRs, leaving the ADRs untouched.
3. **Amendment in place, with a dated record** of what changed and why.

## Decision Outcome

Chosen: **option 3**.

An ADR may be amended in place, and every amendment is recorded in a dated `## Amendments` section at the bottom of the file saying what changed and why.
Its `## Status` is marked so the amendment is visible to somebody who reads no further.

The line runs through the middle of the document, not around it.

**Substance is never amended.**
The decision outcome, the considered options, and the reasons each option won or lost are the record.
Changing what an argument *says* is a new ADR, always, even when the new argument is better.

**Wording may be amended** where the substance is untouched: terminology, a renamed thing, a corrected fact, a dead link, a typo.

The safeguard is not that the line is impossible to cross.
It is that the amendment entry states exactly what changed, so a reader can judge for themselves whether it was crossed, and an amendment that cannot be described honestly in one sentence is a superseding ADR wearing a disguise.

A reversed decision still gets a new ADR that supersedes the old one, and the old one still stays.
Nothing about that changes.

## Consequences

### Good

- Documents that have become wrong can be made right without pretending a settled decision was reopened, which is what superseding would have implied.
- The correction and the reasoning stay in the file a reader is already looking at, rather than in an errata document nobody opens.
- The dated entries make amendment visible and cheap to audit. A file with no `## Amendments` section is verbatim as accepted, which is a stronger guarantee than the old rule actually delivered, since the old rule had no mechanism to prove a file had never been touched.
- It gives ADR-0027's corrections somewhere to go that is proportionate to what they are.

### Bad

- The substance-versus-wording line is a judgement call, and judgement calls erode. Someone will eventually amend a rejected-because entry in a way that changes the argument and describe it as terminology.
- Amendment is easier than superseding, so it will be reached for when superseding is correct. The cost of getting that wrong is a decision quietly changing without a record, which is the exact failure the original rule existed to prevent.
- Git history already recorded every edit, so this adds a second, hand-maintained record that can disagree with the first. It is justified by ADRs being read as rendered documents rather than as a commit log, but it is duplication.
- An ADR amended many times accumulates a tail of entries that is longer than the decision, which is a signal it should have been superseded and will not always be read as one.

### Rejected because

- **Option 1** was rejected because it protects the wrong thing. It treats every sentence in an ADR as part of the record, including sentences that were never decided, and its only remedy is to reopen decisions that are not in question. A rule whose correct application produces an obviously wrong outcome gets ignored rather than followed.
- **Option 2** was rejected because it splits the record. A reader of `0003-license.md` would have to know an errata file exists and think to check it, and corrections that live away from the thing they correct do not get read. It also has all of this option's judgement problems while adding a second place to look.
