# 49. A note's kind is its rank, through a role rather than a number

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

[ADR-0010](0010-mcp-surface.md) said what a note's kind is for, and it was never built:

> Kind affects **ranking and rendering**, not just labelling.
> A `gotcha` surfaces prominently on an entity page and ranks highly in how-do-I search; a `todo` does not pollute those results.
> A purely decorative kind would be chosen carelessly and would be worthless.

Notes rank by `pinned` and then by id, so the only way to make a gotcha come first is to pin it by hand.
Pinning is a scarce resource, because [ADR-0014](0014-agent-context-injection.md)'s context budget is spent on pinned notes, and burning it on something the kind already implies is a waste of the one lever that was meant for exceptions.

It matters more since ideas landed.
An `idea` is a note that is work rather than knowledge, ideas accumulate by design, and every one of them currently competes with a gotcha for the top of an entity page and for a place in a search result.

The open question is what the ranking is derived from.
Hard-coding a table of the seven well-known kinds would rank them and leave every minted kind at the bottom or in the middle, which is precisely the decorative kind [ADR-0010](0010-mcp-surface.md) warns about, arrived at through the back door.

## Considered Options

For what carries the rank:

1. **A hard-coded table** of the well-known kinds to weights.
2. **A number on the mint**, so the operator sets the weight directly.
3. **A role from a small closed set**, with the rank derived from the role.

For how ranking reaches search:

1. **Reweight the relevance score by kind**, multiplying the full-text rank.
2. **Exclude work notes from search**, with an escape hatch.
3. **Demote work notes below every other hit**, ranking by relevance within each group.

## Decision Outcome

Chosen: **a role**, and **demotion** in search.

### Three roles, and each of the well-known kinds has an obvious one

- `warning`: something that will bite you. `gotcha`, `incident`.
- `knowledge`: how the thing works or why it is the way it is. `runbook`, `howto`, `decision`.
- `work`: something not done yet. `todo`, `idea`.

The seven well-known kinds split into these with no awkward cases, which is the evidence that three is the right number rather than a number somebody liked.

A role is what a mint declares, so a minted kind ranks from the moment it exists and there is no such thing as a note kind Dusk has no opinion about.
A kind nobody minted and that is not well known is `knowledge`, which is the middle: ranking it above a gotcha would be wrong, and hiding it would lose something somebody wrote down on purpose.

### The role is the same fact that decides whether a note can be closed

`duskmd.Working` already listed `idea` and `todo` as "the kinds a status means something for. A gotcha is never done".
That list is now a consequence of the role rather than a second hand-maintained list beside it, so minting a `work` kind makes it closable and minting a `knowledge` one does not, with nobody having to remember to update anything.

One fact, three consequences: where a note ranks, whether it is a warning, and whether it can be finished.
This is what makes a role worth asking for at mint time.
Asking for a weight instead would have bought one of the three.

### Ranking applies where the question is "what should I know", not "what is new"

`NotesFor`, which backs an entity page and `get`, orders pinned first, then by role, then by id.
A gotcha reaches the top of a service's page without anybody pinning it, which is the whole point.

`Notes`, which backs the `note` read tool and the homepage's `recent-notes` block, keeps ordering by recency.
That block is named for what it does, and a feed reordered by role is not a feed.
The `note` tool takes a kind filter, and within one kind the role is constant, so ranking there would be a no-op in the case it is actually used.

This is a boundary somebody has to know about, and it is stated here because two orderings that look the same and are not is exactly the kind of thing that gets "fixed" into one.

### Search demotes work rather than hiding it or reweighting it

A work note ranks below every non-work hit, and hits rank by relevance within each group.

[ADR-0010](0010-mcp-surface.md)'s words are that a todo "does not pollute those results", and never outranking anything else is what that means operationally.
With a limit, a demoted todo falls off the end of a busy search, which is the intended effect.

Warning notes are **not** promoted, and this is deliberate.
Promotion would put a barely-matching gotcha above an exact entity match, which makes the search worse in service of a rule about notes.
The other half of [ADR-0010](0010-mcp-surface.md)'s sentence, that a gotcha "surfaces prominently", is served by rendering rather than ranking: a note hit shows its kind, and on an entity page warnings are already first.
Ranking and rendering were named as two things in that sentence, and they stay two things.

## Consequences

### Good

- A gotcha outranks a todo with nobody pinning anything, which is what pinning was being spent on.
- Pinning goes back to meaning "this one, specifically", which is what the context budget in [ADR-0014](0014-agent-context-injection.md) assumes it means.
- A minted note kind ranks from the moment it exists, so [ADR-0010](0010-mcp-surface.md)'s "a purely decorative kind would be worthless" cannot happen by omission.
- `duskmd.Working` stops being a second list that has to be kept in step with the first.
- Search stays ordered by relevance, so nothing about it gets worse for entities, which are most of what it answers.

### Bad

- **Three roles is a taxonomy, and taxonomies are guesses.** `incident` is a warning today and somebody will reasonably want it as history. The escape is to mint it with a different role, which works, but the well-known defaults are opinions baked in.
- Two orderings exist for notes, one by role and one by recency, and which applies depends on which question was asked. Anybody who does not know that will read one of them as a bug.
- A work note that is genuinely the best answer to a search is now hard to find, and nothing says it was demoted. Exclusion would at least have allowed saying how many were hidden.
- The default of `knowledge` for an unminted kind means a misspelled kind, which is by definition unminted, ranks in the middle rather than being obviously wrong.
- Ranking happens in Go rather than in SQL, because the rank rule belongs to `vocab` and encoding it a second time as a `CASE` would be the drift this repository has already had twice. The cost is that `NotesFor` reads its rows before ordering them, which is fine for the notes on one entity and would not be for a query that had to page.

### Rejected because

- **A hard-coded table** was rejected because it ranks the seven kinds that exist and leaves every kind an operator invents unranked, which is the decorative kind [ADR-0010](0010-mcp-surface.md) warns against reached by accident rather than on purpose.
- **A number on the mint** was rejected because a weight is meaningless in isolation, so nobody can answer whether 70 is high without reading every other kind first, and two people minting on different days will not agree. It also buys only the ranking, where a role also answers whether the note is a warning and whether it can be closed.
- **Reweighting the search score** was rejected because multiplying a relevance score by a kind weight produces an order nobody can reason about or test against, and it tunes a full-text ranking by a fact that has nothing to do with relevance.
- **Excluding work from search** was rejected, narrowly. It is closer to [ADR-0010](0010-mcp-surface.md)'s literal "does not pollute", and it could report how many it hid. It loses a real answer: somebody searching for the thing they wrote a todo about should find the todo, and an escape hatch nobody knows about is not an escape hatch. Demotion gets the same result for a busy search and keeps the answer for a quiet one.

## Amendments

None yet.
