# 57. A section is charged what it printed, and the inventory leads with what is maintained

Date: 2026-08-15

## Status

Accepted

## Context and Problem Statement

[ADR-0050](0050-what-the-context-budget-buys-first.md) decided what the context budget buys first, and the allocation it shipped collapses under the catalog it was written for.

`dusk_context` is the first call a session makes and the only one guaranteed to happen, so everything an agent does afterwards depends on whether it oriented properly.
Driven against a real estate it answered in **900 bytes of its 8000 byte budget**: two pinned note titles, an overflow line reading "and 1 more it declares" printed with no heading above it, and "13 more kind(s) not listed" naming none of the thirteen.
An agent starting work in that repository was oriented by nothing.

Four separate faults produce that answer, and they compound.

**A section is granted what it wants and charged nothing back for what it did not use.**
`wants()` is the sum of every entry printed whole.
A section holding two long pinned notes wants ten thousand bytes, is granted the cap, prints two one-line shorts because the whole notes do not fit, and spends two hundred and fifty.
The other nine hundred and fifty of every thousand granted is never reclaimed, because `render` returns what it spent and the caller discards it.
Every section below it in priority order sees an exhausted budget.

**The share cap does not do what it says.**
ADR-0050 states "no section takes more than half the spendable budget until every other section has had a turn", and the answer has four sections.
Two sections at half the budget each is the whole budget, so the two pinned-note sections can starve both remaining sections before either is reached.
Half is the right shape of answer and the wrong quantity to bind against.

**The inventory is ordered alphabetically, which is to say arbitrarily.**
In a service catalog the elided kinds were `service` and `vault-note`, because `s` and `v` sort last.
The orientation listed every airport and omitted the kind the product exists to catalog.
Alphabetical order was never chosen; it fell out of `sortedKeysOf` over a map.

**An overflow line counts what it left out and does not name it.**
"13 more kind(s) not listed" is honest and useless.
A kind name is roughly ten bytes and the count that replaces it is two, so the cheapest possible fix was not taken because nobody asked what the line was for.

Two smaller defects sit in the same path.
An overflow line prints with no heading, because the heading is charged against a budget that can be smaller than the heading.
And a ref reaching the index from two sources is listed twice, because `List` does not deduplicate rows the way `Declared` does.

## Considered Options

For reclaiming what a section does not use:

1. **Grant by `wants()` and redistribute the difference in a later pass.** Keeps one accounting of what a section would like, and adds a correction after it.
2. **Charge a section what it printed.** Render as the budget is handed out, and subtract the measured spend rather than the grant.
3. **Predict the degraded size instead of the full one.** Compute what a section would spend at a given budget without rendering, and allocate against that.

For the share cap:

1. **A share of the whole budget**, as shipped.
2. **A share of what remains when a section's turn comes.**
3. **An equal split across sections**, which [ADR-0050](0050-what-the-context-budget-buys-first.md) already rejected as its own option 2.

For ordering the inventory:

1. **Alphabetical**, as shipped.
2. **By count, largest first.**
3. **By role, then by count.**
4. **Breadth first**: every kind's name and count printed before any kind's refs.

## Decision Outcome

Chosen: **charge a section what it printed**, cap it at **a share of what remains**, and order the inventory **by role, then by count**, with overflow lines that name what they dropped.

### A section is charged what it printed

`spend` hands the budget out in priority order and renders as it goes, subtracting the measured spend rather than the grant.
A section that degrades to shorts hands the difference to the section below it in the same pass, because the difference was never taken out of the pool.

That is one line of accounting and it is the whole of the collapse.
The same catalog that answered in 900 bytes now answers in **6949**, and the inventory that printed nothing prints every kind and every ref it has.

Re-rendering a section against more room is how it grows, so `render` is called more than once per section.
It is a pure function of its budget over at most a few hundred entries, and the answer is built once per session, so the cost is not worth an accounting scheme to avoid.
Greedy packing is not quite monotonic, since a full entry that only fits at the larger budget can crowd out two shorts that fitted at the smaller one, so a render that spends less than the section already had is undone.

### The share binds on what is left, not on the whole

A section may take half of **what remains when its turn comes**.
Half of the remainder can never be all of the remainder, so there is always something for the section below, at any number of sections.
The second pass then hands on everything the first left, so the cap still costs nothing when only one section has anything to say.

This also answers ADR-0050's own complaint about itself, that "the share cap is a second number with no principle behind it beyond not all of it".
There is now a principle: it is the rule that guarantees every section a turn, and the number is which fraction of a turn the first section gets.

### The inventory leads with what the operator maintains

Kinds carry a role since [ADR-0048](0048-the-kind-vocabulary.md): `infrastructure` is something somebody maintains, `reference` is a fact about the world.
That is exactly the distinction an orientation wants, and it was already being computed for drift and ignored here.

Infrastructure kinds rank above reference kinds, and within a role the kind carrying most of the estate goes first.
An estate whose orientation opens with airports and never reaches services is ordered backwards, and no amount of budget fixes it.

The rank order comes from `vocab.Roles`, which already lists the roles in rank order, rather than from a second list in the context code.
Not minting still changes nothing, per [ADR-0048](0048-the-kind-vocabulary.md): an unminted kind carries the default role, so an estate that has never minted sees the same ordering it had, by count instead of by spelling.

### A kind left out is named, not counted

An overflow line names the entries it dropped, up to a bounded number, and counts the rest behind them.
A count cannot say `service`, and the whole purpose of the line is to tell an agent what to ask for.

The bound is what makes this affordable.
The room for an overflow line is reserved before anything is spent, so an unbounded line means a catalog with two hundred kinds reserving a quarter of its budget for an apology it will probably never print.
Because the reservation is charged whether or not the line prints, it is taken against the most expensive line the section could produce: the longest names it might carry, with every remaining entry counted behind them.

### A heading is reserved beside the line under it

An overflow line is meaningless without the thing it is overflowing from.
The heading is reserved with the overflow line rather than charged against the section's budget, so a section that wins no room at all still prints its heading and says what it dropped.
This is the same argument ADR-0050 made for reserving the overflow line, applied to the only other part of a section that is not content.

### One ref is one row

The inventory deduplicates by ref, and the count above it is of what is listed below it.
Two repositories declaring the same entity is a real state that [ADR-0033](0033-graph-integrity.md) reports and does not resolve, and an ingester observing what a repository declares is the ordinary case rather than an error.
Neither is a reason to print the same ref twice in a list of what exists.

The deduplication is in the context builder rather than in `List`, because `List` returning every row is what lets `integrity` see both declarations.
Nothing else that renders a list of entities to a human is currently exposed to the duplicate, but the next thing that is will need the same treatment, and that will be the moment to decide whether the index should offer a deduplicated read.

### One sentence saying the catalog acts

Nothing in the orientation mentioned actions, `invoke`, or plugins.
An agent that never happened to `get` an entity carrying an action never learned that the catalog could do anything at all, which makes every installed plugin invisible until something stumbles into it.

The tail now names the kinds that carry actions, in one sentence, and only when a plugin manager is configured.
`invoke` is registered only when one is, so a deployment without plugins would otherwise be told to call a tool that is not there.

## Consequences

### Good

- The budget is spent. The measured case went from 900 bytes to 6949, and what it bought is the entire inventory: every kind, every ref, in an estate where none of it printed before.
- The failure it fixes is the worst one in the product, because `dusk_context` is the only call every session makes and an agent cannot tell a starved orientation from an empty catalog.
- The cap now has a principle rather than a number, and it holds for any number of sections rather than for two.
- A role earns a second consequence. [ADR-0048](0048-the-kind-vocabulary.md) argued that minting must change behaviour or it is ceremony, and this is a second place where it does, visible in the first thing an agent reads.
- The overflow line becomes useful rather than only honest: an agent that reads a name can ask for it, and a count is not a thing anybody can act on.

### Bad

- Sections are rendered several times each. It is cheap and it is still work done to answer a question that could in principle be answered by arithmetic, and a future entry type whose rendering is expensive would make this the wrong shape.
- The reserve is still not redistributed. A section that drops nothing pays for an overflow line it never prints, which is a few hundred bytes of the budget held permanently against a line that may not exist. It is the same class of waste this ADR exists to fix, one order of magnitude smaller, and left alone deliberately rather than missed.
- Ranking by count within a role gives the names to the largest kinds and the counts to the smallest, which is the opposite of what an operator with three hand-declared datastores and four hundred observed containers may want to read.
- The ordering is a second policy judgement in the one place [ADR-0050](0050-what-the-context-budget-buys-first.md) already noted is the only place Dusk judges catalog content against other catalog content. There is still no setting, and there are now two ways for it to be wrong for somebody.
- A wrongly minted role now costs an operator their orientation as well as their drift report. Minting `service` as `reference` pushes it below every airport, and nothing will say so.
- Deduplicating in the context builder means two readers of `List` have two behaviours, and the second one to need this will copy it rather than find it.
- The sentence about actions is computed from what the kinds in the inventory offer, so a kind that carries actions and has no entities in the catalog is not mentioned.

### Rejected because

- **Redistributing after granting by `wants()`** was rejected because it keeps two numbers for one thing and stays wrong between them. The pass that corrects the grant has to guess what the render will spend, which is the same guess that produced the bug, and every later change to how an entry degrades reopens it.
- **Predicting the degraded size** was rejected as the same guess with more code. The only thing that knows what a section will print is the thing that prints it, and a predictor that disagrees with the renderer is a bug that shows up as a budget silently under-spent, which is exactly the failure being fixed and is invisible from outside.
- **A share of the whole budget** was rejected because it does not bind the thing it claims to. It is correct for two sections and wrong for four, and the answer has four.
- **An equal split** stays rejected for [ADR-0050](0050-what-the-context-budget-buys-first.md)'s reason: ratios describe a catalog nobody has.
- **Alphabetical** was rejected because it is not an ordering, it is the absence of one, and it puts the answer at the mercy of what a kind is called.
- **Count alone** was rejected because it is what produced the complaint. A plugin emitting four hundred airports outranks everything an operator declared by hand, and the count is a fact about a plugin's output rather than about what matters.
- **Breadth first** was rejected here and not on its merits. [ADR-0050](0050-what-the-context-budget-buys-first.md) already records it as the better shape for an agent that wants to know what exists more than what it says, and naming what an overflow dropped buys most of that for one line of change. It stays available, it is still a change to one loop, and it is a larger decision than this one, because it changes what every section does and not just the inventory.

## Amendments

Amendment policy: [ADR-0028](0028-amending-adrs.md).

### 2026-08-18: every section names what it dropped, not only the inventory

"Nothing is dropped without being named" was written as a rule and built for one section.

The inventory called `names(dropped)`. The two pinned-note sections and the declared-refs section printed `len(dropped)` and stopped, so an agent was told `2 more pinned note(s) are not listed` and given nothing to ask for.

The reasoning this ADR already records is the reasoning against that: *a count cannot say `service`*. A count cannot say `.dusk/gotcha-5b1bcd61.md` either, and the case is worse, because an id is not merely a better label. It is the argument `note` takes. [ADR-0050](0050-what-the-context-budget-buys-first.md) justifies the named tier as "a note an agent knows exists is one it can ask for", and a counted note was one the agent knew existed and **could not** ask for. Until `pinned` became a working read filter on the same day, there was no way to recover them at all.

All four sections now name what they dropped. Nothing new was needed: `reserve` already sets aside the longest overflow line a section could print, and `listed` already caps it at twelve names with the rest counted behind them, because that room is reserved whether or not the line ever prints. A note's and a ref's `name` is backticked, since an overflow naming one is handing over an identifier to paste back.

The gap survived because the rule was tested where it was implemented. `TestADR0057_TheOverflowNamesTheKindsItLeftOut` asserts it of the inventory and nothing asserted it of the sections that mattered more.

### 2026-08-18: the cap names the call that answers with the rest, and everything enumerated is a list

Naming what was dropped left the same defect one level down, in the cap that makes the naming affordable.

`listed` names twelve and then said `and 13 more`, which is a count that cannot say which note, reintroducing exactly the problem the amendment above fixed. It was visible immediately once the overflow started naming ids: the reader can reach twelve of them and has nothing at all for the remainder.

Every overflow now names the call that answers with all of them: `note` with `pinned: true` for either pinned section, `search` for what a repository declares, `kinds` for the vocabulary. The cap stays, because the reserve is paid whether or not the line prints, and it is now a display limit rather than an information limit.

Separately, everything the context enumerates is a markdown list rather than a comma run inside a sentence: the overflow entries, the kinds carrying actions, the plugins holding entity-less capability, and the refs under each kind in the inventory, which were one long comma-joined line per kind. The inventory pays a few bytes a ref for it and the budget absorbs that by degrading a kind to its count, which is the trade this ADR already makes everywhere else.
