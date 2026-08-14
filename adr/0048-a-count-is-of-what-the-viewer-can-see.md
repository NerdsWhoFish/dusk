# 48. A count is of what the viewer can see

Date: 2026-08-13

## Status

Accepted. Extends [ADR-0012](0012-viewing-auth.md) and [ADR-0036](0036-deriving-what-a-viewer-sees.md).

## Context and Problem Statement

[ADR-0012](0012-viewing-auth.md) settled that viewing authorization derives from repository access rather than from a permission model inside Dusk, and [ADR-0036](0036-deriving-what-a-viewer-sees.md) implemented it.
A signed-in viewer sees the entities backed by repositories they can read, an entity they may not see answers exactly as one that does not exist, and observed entities stay hidden unless `DUSK_OBSERVED_VISIBLE_TO_ALL` says otherwise.

Per-entity filtering fell out of that, as [ADR-0012](0012-viewing-auth.md) promised it would.
Three reads did not, and [ADR-0036](0036-deriving-what-a-viewer-sees.md) recorded them as a gap rather than closing it: drift, integrity, and the kind counts rendered on the homepage and in `dusk_context`.
They are the only reads a signed-in person sees unfiltered.

Two of the three are ordinary leaks with an obvious fix.
Drift names what was declared and is nowhere to be found, and the notes pointing at refs nothing holds.
Integrity names refs declared twice, the repositories and files holding each copy, and the targets of relations nothing declares.
Every one of those is a ref, a repository name or a path from a repository the viewer cannot open.

The kind counts are not obvious, and they are why this is a record rather than a commit message.
A count leaks less than a list and still leaks.
A viewer learning there are fourteen `datastore` entities they cannot see has learned that a datastore tier exists, roughly how big it is, and that it is not theirs.
This catalog already holds entities from 42 repositories, and a plugin about to be configured will add roughly 250 more from the network layer, so the distance between what an operator sees and what a restricted viewer sees is about to be large.

There is a genuine trade-off underneath.
A count of everything is honest about the estate.
A count of the visible half is honest about scope.
Only one of them can be the number on the page.

## Considered Options

1. **Count what the viewer can see.** Every tally and every report is computed over the visible half of the catalog.
2. **Count everything, with the invisible part not broken out.** A grand total over the whole estate, and per-kind counts only for what is visible.
3. **Count everything, and say what is hidden.** "14 datastores, 11 of which you cannot open."
4. **Answer nothing.** Drift, integrity and the kind counts are simply absent for a restricted viewer.

## Decision Outcome

Chosen: **option 1**.

A restricted viewer's catalog is a sub-catalog.
Every read that cannot be filtered after the fact is computed as though the half they cannot see does not exist, and `index.Visibility` becomes a required argument on each of them.

### Because ADR-0036 already decided it

"Invisible is indistinguishable from absent" is an accepted rule, and a count that includes what a `get` would answer 404 for contradicts it.
The contradiction is itself the leak.
Subtract the list from the count and the difference is the number of hidden entities, per kind, which is more precise than most things this is trying to protect.
A rule that holds for one read and not the next is not a rule, it is a habit.

### A count on a page is a way in, not a fact

The kinds block exists to be clicked.
A count of fourteen that opens a list of three is a broken link, and explaining it means explaining somebody else's GitHub access.
Being honest about the size of the estate is a real thing to want, and it is an operator feature that should be built deliberately rather than arrived at by leaving a tally unfiltered.

### The comparison happens inside the viewer's half

Drift and integrity are not lists that can be narrowed afterwards.
A duplicate declaration is a count of copies: filtering the answer would report a duplicate whose second copy sits in a repository the viewer cannot be told about, and name it in the same breath.
So the predicate goes into the query.
`duplicates` groups over visible rows only, so a ref with one visible copy is not a duplicate.
`danglingRelations` and the note-ref half of drift resolve a target against visible entities only.
`compare` matches a declaration against observations the viewer can see, on both sides and through `observed_as`.

That is the split this record adds to [ADR-0036](0036-deriving-what-a-viewer-sees.md): **a list is filtered where it is rendered, and a count where it is computed.**
Filtering after the query is what keeps an unrestricted viewer paying nothing, and that still holds, because the clause is empty for them and the SQL is the SQL they had before.
It simply cannot be stretched over an aggregate.

### Observed entities, in both settings

An observed entity has no repository to derive access from, which is why `DUSK_OBSERVED_VISIBLE_TO_ALL` exists.
Both of its settings now have a defined answer rather than an accident.

**Off, which is the default.**
A restricted viewer sees no observation at all, so no kind is watched, and [ADR-0038](0038-what-drift-may-say.md) keeps drift silent about a kind nothing watches.
Drift is therefore empty for them, and integrity reports no orphaned observation scope, because naming one says an ingester ran and what it found.
This is the correct answer and it will read as a broken feature.
It is still better than the alternative that same query would otherwise produce, which is every declaration reading as missing because the thing that observes it is invisible.

**On.**
Observations join the counts, and both directions of drift work.
A declaration in a repository the viewer cannot read no longer claims its observation, so an entity somebody else declared reads to them as running and undeclared.
That is a false positive, and it names nothing they could not already see.

## Consequences

### Good

- [ADR-0012](0012-viewing-auth.md)'s promise holds all the way through. There is still no permission model inside Dusk, and nothing new to administer: these reads changed shape, not authority.
- One rule covers every derived read, stated once and testable: what a viewer is told is computed from what a viewer can see.
- An unrestricted viewer pays nothing. `Visibility.clause` returns the empty string, no predicate is added, and every single-operator deployment runs the queries it ran before.
- `Visibility` is a required argument rather than an option, so a fourth aggregate read cannot quietly ship unfiltered. That is the failure [ADR-0036](0036-deriving-what-a-viewer-sees.md) predicted when it rejected leaving the filter for later.
- One predicate serves every table. An observation is recognised by the reserved `ingester:` prefix on its repository, which relations and notes carry even though they have no `observed` column.

### Bad

- **A restricted viewer cannot tell a small catalog from a small slice of a large one.** They may not know there is anything to ask for access to, which makes [ADR-0012](0012-viewing-auth.md)'s already recorded "sees an empty catalog" consequence quieter rather than louder.
- Two people looking at the same homepage see different numbers, and that reads as a bug until somebody explains derivation. Nothing on the page explains it beyond the banner saying the view is filtered.
- Drift and integrity now answer a per-viewer question. A relation whose target is declared in a repository the viewer cannot read is reported to them as dangling, and its detail text says the target may live in a repository Dusk cannot see, which is now true in a second way it does not distinguish.
- Under the default observed setting, drift is empty for every restricted viewer. Anybody who signs in to get a narrower view also loses that report entirely.
- Every aggregate read grows a predicate for a restricted viewer, and the entities table's indexed `observed` column is no longer what answers it. At this size that is not measurable, and it was chosen for having one predicate rather than two.
- The homepage's `reads` block still names every repository and the overview's `declaring` count still counts them. Same class of leak, different block, and recorded in `docs/status.md` rather than closed here.
- Notes are filtered by repository in the drift report and nowhere else. `search` still returns a note from a repository the viewer cannot read, on the reasoning that a note travels with the entity it is about, which is not reasoning that survives this record.
- The agent surface is unaffected, because a shared bearer token carries no identity to derive anything from. `mcp.Server.viewer` is the one place that will change when it does, and until then it returns `Unrestricted` and says why.

### Rejected because

- **Option 2** was rejected because "not broken out" is not a property two numbers on one page can keep. The grand total leaks the size of the estate on its own, and subtracting the visible per-kind counts from it gives back the hidden count exactly.
- **Option 3** is the honest option, and it is the leak written out rather than avoided. [ADR-0036](0036-deriving-what-a-viewer-sees.md)'s asymmetry decides it: hiding something that should have been visible is a complaint, and showing something that should have been hidden is an incident. A deployment might one day want to opt into it deliberately, which is not the same as arriving at it by leaving a query unfiltered.
- **Option 4** leaks nothing and needs no SQL at all, and it makes signing in punitive. A person who signs in to narrow their view would lose features an anonymous holder of the shared token keeps, and per-entity filtering would stop falling out for free.
- **Filtering the rows these reads return, rather than the rows they read**, is the cheap fix and it is wrong for all three. It cannot make a count smaller without making it disagree with itself, and for a duplicate declaration it would keep the problem while naming the repository and file of a copy the viewer may not know about.
