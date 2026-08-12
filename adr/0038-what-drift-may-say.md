# 38. Drift only speaks where something is watching

Date: 2026-08-12

## Status

Accepted

## Context and Problem Statement

[ADR-0034](0034-ingesters-in-tree-first.md) put declarations and observations in one index and made them comparable, which is what makes drift possible.
It did not say what the comparison is allowed to conclude, and the obvious reading turns out to be wrong in a way that shows up on first contact.

The obvious reading is that a declared entity nothing observed is missing.
Run it against a real catalog and the first thing it reports is the repositories.

A `repository:` entity has no observer.
Neither does a `team`, a `person`, or anything else describing something that is not a running process.
The Kubernetes ingester reports services, hosts and clusters, and by this reading every entity of every other kind is permanently, unfixably drifted.

The same shape had already been caught once at the whole-catalog level: with no ingesters running at all, drift returned the entire catalog, so a global guard was added to keep it silent.
That guard was the right instinct applied at the wrong granularity.
Observation is not on or off for a catalog, it is on or off per kind, and it will get more granular still as ingesters multiply.

There is a second case with the same cause.
Declaring an entity at the exact ref an ingester reports, which is precisely what an operator should do after seeing it in the undeclared list, was reported by `integrity` as a duplicate declaration.
The catalog was telling operators that doing the right thing had broken something.

Both are the same error: treating the absence of a signal as a signal.

## Considered Options

1. **Report everything, let the reader filter.** Drift names every declared entity nothing observed, and the UI or the operator decides what is noise.
2. **Configure which kinds are watched.** An explicit list, set by the operator, of kinds drift is entitled to judge.
3. **Derive it from what is actually observed.** A kind is watched if at least one observation of that kind exists, and drift stays quiet about every other kind.
4. **Have ingesters declare their coverage.** Each ingester states the kinds it is responsible for, and drift judges exactly those.

## Decision Outcome

Chosen: **option 3**.

A declared entity is reported missing only if some ingester has observed at least one entity of the same kind.
The undeclared half is unaffected: something running and written down nowhere is actionable regardless of kind, and needs no such test.

### Absence is only evidence where there is a witness

"I did not find it" and "I was not looking" are different statements, and [ADR-0011](0011-ingester-scheduling.md) already forbids conflating them for a *failed* ingester run.

This is that same rule one step further out.
A failed run must not look like a deletion; a kind nobody covers must not look like an absence.
Both collapse into "the catalog reports what it does not know as though it knew it", which is the failure [ADR-0017](0017-engineering-policy.md) names as the only one that matters.

This is the load-bearing rule and it gets a named test.

### Derived beats configured, because configuration goes stale silently

Option 2 puts the burden on the operator to keep a list in step with what is deployed, and the failure mode is invisible: a stale list either suppresses real drift or produces the noise this ADR removes, and nothing indicates which.

Option 4 is better and is where this likely ends up, since an ingester declaring "I cover services in this cluster" is more precise than inferring it from what it happened to return.
It is not chosen now because the interface it requires does not exist and inferring from observations needs no interface at all.
The inference is a strict improvement on today with no new surface, and it is compatible with option 4 replacing it later.

### The global silence rule was a special case of this one

With this in place, a catalog with no ingesters running observes no kinds, so no declared entity is judged and drift is silent.
The separate guard that produced that behaviour is removed rather than kept alongside, because two implementations of one rule drift apart, which is a failure this repository has already had twice.

### Integrity gets the same correction

`integrity` grouped every row sharing a ref, which was written before observations shared the table.
It now groups on `observed` as well, so a duplicate means two sources of *equal standing*: two repositories declaring one ref, or two ingesters observing one ref.

A declaration sitting on its own observation is not a conflict.
[ADR-0034](0034-ingesters-in-tree-first.md) already resolves it deterministically to the human, and it is the state the catalog is trying to reach.
Recorded as an amendment on [ADR-0033](0033-graph-integrity.md).

## Consequences

### Good

- Drift is usable on a real catalog. The report is now things an operator can act on rather than a list of kinds nobody will ever observe.
- Declaring an observed entity clears it from drift and reports no new problem, so the loop the undeclared list invites actually closes.
- The rule is derived, so an operator adding an ingester gets drift coverage for its kinds with nothing to configure.
- One rule replaces a special case, and `Drift` no longer needs a separate query to decide whether to speak.

### Bad

- **Removing the only ingester for a kind makes drift go quiet about it instead of loud.** Every service declared stops being checked, and the report shrinks rather than filling with alarms. This is the direct cost of the decision and the case where option 1 would have served better.
- Coverage is inferred from what an ingester returned, not what it is responsible for. An ingester that can see services but returns none this run makes its kind unwatched for that run.
- A kind is global, so an ingester covering one cluster makes `service` watched everywhere. A service declared in an unwatched cluster is reported missing, and the fix is the per-ingester coverage of option 4.
- Two ingesters observing one ref is now reported as a duplicate. Correct, but nothing yet resolves it beyond sort order.

### Rejected because

- **Option 1** was rejected because a report whose first page is structurally unfixable is one nobody reads twice, and the noise arrives exactly when a new operator is deciding whether to trust it.
- **Option 2** was rejected because it fails silently in both directions and asks the operator to maintain a list Dusk can derive.
- **Option 4** was rejected for now on sequencing, not merit. It needs an ingester interface change to carry coverage, and option 3 gets most of the benefit today without one. It stays the intended destination.
