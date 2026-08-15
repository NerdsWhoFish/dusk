# 54. A kind's role is corrected by minting it again

Date: 2026-08-15

## Status

Accepted

## Context and Problem Statement

[ADR-0048](0048-the-kind-vocabulary.md) made a role the thing that changes behaviour, and gave minting exactly one refusal: a name that is already the vocabulary's after normalization.

Those two decisions collide, because the vocabulary that refusal is checked against is **derived plus minted** rather than minted alone.
Every kind anything carries is already in it, with the default role for its namespace.
So `airport` is in the vocabulary as `infrastructure` the moment one airport is declared, and minting it as `reference` is refused with "the entity kind `airport` already exists, for infrastructure. Nothing was minted".

The kind whose role is most worth correcting is exactly the kind that cannot be minted: the one nobody chose a role for.
On a real catalog that was 66 travel-log entities in the drift report, under a role no human ever picked, and the only fix was editing `.dusk/kinds.md` by hand in the config repository.
That works, and it is not what the tool tells you to do, which is the failure mode [`docs/philosophy.md`](../docs/philosophy.md) calls out: an answer that names a recovery which does not exist is worse than no answer, because an agent follows it, fails, and concludes the tool is broken.

The refusal took its own advice down with it.
It ends "if they are the same thing, mint `service` with `Service` in its aliases", and minting `service` was refused, because `service` exists.
The one documented way out of the one refusal was itself refused.

## Considered Options

1. **Keep the refusal**, and document editing `.dusk/kinds.md` by hand.
2. **A distinct call, or a distinct argument**: `kinds(reRole: …)`, or a `force` flag on the existing one.
3. **The same call**: minting a name already spelled that way corrects the kind rather than being refused.
4. **Option 3, with the role optional**, so a correction can add an alias without restating what the kind is for.

## Decision Outcome

Chosen: **option 3**, with the role still required and aliases added rather than replaced.

### The refusal was about a second spelling, and still is

[ADR-0048](0048-the-kind-vocabulary.md)'s reasoning for its one refusal is precise and unchanged: minting `Service` where `service` exists "is not extending a vocabulary, it is putting two rows in it that mean the same thing".

Re-roling is not that.
`airport` and `airport` are one row, and one row is what the operator ends up with.

So the refusal narrows to what it was always arguing about: **a name that normalizes onto a kind that exists but is spelled differently**.
The same spelling is a correction.
The answer to a refusal still names the alias that would say it properly, and that call now works.

### The same call, because it is the same read that authorizes it

[ADR-0048](0048-the-kind-vocabulary.md) argues `kinds` is one tool because "the read is what issues the proof token the write needs", and that "an agent cannot invent `svc` without first having been shown `service`".

That argument is stronger for a correction than for a mint.
The vocabulary read prints every kind with its current role, so a correction is only ever made by a caller that has just been shown the role it is about to change, in the same answer that carried the token.
A separate call or a `force` flag would add surface without adding a single check that the token does not already make.

The tool count is a product constraint from [ADR-0010](0010-mcp-surface.md), and one tool stays one.

### A correction carries the same proof, because it is not cosmetic

Changing a role changes what `drift` reports, which is the whole reason a role exists.
It is therefore a write like any other and presents the token from reading the vocabulary, exactly as a mint always has ([ADR-0009](0009-proof-tokens.md)).

Nothing new is needed for this: the vocabulary is one file, the token is that file's hash, and a correction is an update to it.

### Aliases are added, never replaced

A correction states one fact.
Replacing the alias list would mean correcting a role silently drops what the kind is also called, which is the same class of loss as a note update blanking its refs.

So `Merge` in `vocab` sets the role and appends aliases that are not already there, compared the way a collision is judged so `SVC` is not added beside `svc`.

### The role stays required

Option 4 is the tempting one and is rejected on two counts.

[ADR-0048](0048-the-kind-vocabulary.md) requires a mint to carry a role because "a purely decorative kind would be chosen carelessly and would be worthless", and an optional role reintroduces a call that mints something meaning nothing.

The second reason is mechanical.
An omitted role can only mean "leave what is there", and what is there is the *file*, while the surface that would have to validate the call reads the *index*.
A kind derived but not minted has no row to leave alone, so an omitted role would have to be resolved against a file the caller has not read, or written as empty and produce a vocabulary file that no longer parses.

The cost is one word restated by a caller that has just been shown it.

### A mint that changes nothing writes nothing

Saying what the file already says answers with that, and commits nothing.
A commit of an identical file is noise in a history somebody reads.

## Consequences

### Good

- The role of a derived kind is correctable through the tool, which is the case [ADR-0048](0048-the-kind-vocabulary.md) was written for and the one it could not reach. An estate whose drift report opens with a hundred airports is one call away from silence, as that ADR promised.
- The refusal's own advice works: `service` with `Service` in its aliases is now a call that lands.
- The refusal that remains is the one with an argument behind it, and it is easier to explain: a different spelling is refused, the same spelling is a correction.
- Nothing about the gate changes. A correction is authorized by the read that showed the current role, so it cannot be made blind.

### Bad

- **A careless mint now changes a role instead of being refused.** The refusal was accidental protection and it is gone. What replaces it is weaker on paper: the answer says what it changed, and the token proves the vocabulary was read.
- The answer's "was `infrastructure` and is now `reference`" is computed from the index, which lags the file by a reconcile. Two corrections in quick succession can therefore report the older role in the second answer, while writing the right one.
- A caller adding an alias has to restate the role, so it can carry a stale one and overwrite a correction somebody else just made. The proof token catches exactly this: the file moved, so the token is stale and the write is refused with the call that re-reads it. It is caught rather than prevented.
- Two agents correcting the same kind still collide on the blob sha, which is [ADR-0048](0048-the-kind-vocabulary.md)'s own cost for one file holding the whole vocabulary, unchanged here.
- A role is still a judgement Dusk cannot check, so a correction can be as wrong as the original was.

### Rejected because

- **Keeping the refusal** was rejected because it leaves the tool telling an operator to do something that does not work, while the thing that does work is hand-editing a file in another repository. That is the exact shape of defect this repository treats as worse than an outright failure.
- **A distinct call or a `force` flag** was rejected because it buys no check the proof token does not already make, and it spends surface [ADR-0010](0010-mcp-surface.md) treats as a product constraint. A flag would also read as "I know this is wrong", which is the wrong description of correcting a role nobody chose.
- **An optional role** was rejected because it re-admits a mint that means nothing, and because "leave what is there" is a fact about the file while the call is validated against the index. The two disagree for exactly as long as a reconcile takes, which is when it would matter.
