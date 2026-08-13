# 45. Drift is a maintenance queue, not an inventory

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

[ADR-0038](0038-what-drift-may-say.md) settled what drift is allowed to conclude and left both directions in the report.
Its exact words: "The undeclared half is unaffected: something running and written down nowhere is actionable regardless of kind, and needs no such test."

That was true when the only ingester was Kubernetes.
Everything it saw was infrastructure, and infrastructure nobody had written down was a real gap.

It stopped being true the moment a plugin observed a domain where declaring each entity is meaningless.
On a real catalog today drift opens with 115 rows of "running and undeclared", and the overwhelming majority are airports and flights that one plugin emits.
Nobody is ever going to write a `dusk.md` for Boston Logan.

The declared half is the actionable one and it is buried underneath.
A report where the rows worth acting on are outnumbered ten to one by rows nobody will ever act on is a report nobody reads twice, and an unread report is the same as no report.

The operator's own framing is the clearest statement of the distinction.
An observed entity is a referenceable fact about reality.
A declared entity is something being maintained, and a declaration that no longer holds is work.
Only the second is a queue.

There is a second case with the same shape, currently reported somewhere else entirely.
A note carries `refs`, and `integrity` reports a note whose ref resolves to nothing as a graph problem.
But the common way a note comes to point at nothing is not a typo, it is the subject being removed.
That is decay over time, which is drift, and it is exactly what an operator wants to see in the same breath as "this declaration no longer holds".

## Considered Options

1. **Leave it and filter in the UI.** The API keeps returning both, and each surface decides what to show.
2. **Rank or cap the undeclared half.** Keep both directions, but bound the noisy one so it cannot swamp the report.
3. **Default to the declared half, with a flag for the other.** Drift answers "what have I written down that no longer holds", and asking for the observed direction is explicit.
4. **Let a plugin declare whether its kinds are worth declaring.** An ingester states that airports are reference data and services are infrastructure, and drift judges accordingly.
5. **Drop the undeclared direction entirely.**

## Decision Outcome

Chosen: **option 3**, with note refs folded into the same report.

`Drift` takes a `DriftFilter`. The zero value answers with what the catalog claims and reality does not support:

- an entity declared and nowhere to be found, unchanged from ADR-0038 including its rule that absence needs a witness
- a note whose ref nothing holds

`DriftFilter{Undeclared: true}` adds what is running and written down nowhere.

### The two directions are different questions and only one is work

Filtering per surface (option 1) puts the same decision in three places and they drift apart, which this repository has already had happen twice.
It also leaves every API consumer with the original problem.

Capping the noisy half (option 2) makes the report shorter without making it better.
A truncated list of things nobody will act on is still a list of things nobody will act on, and now it is also lying about its size.

Dropping the direction entirely (option 5) throws away a genuinely good answer.
"What is running here that I have not written down" is the right question on first contact with an estate, and it is how somebody documents one.
It should be one flag away, not gone.

### Option 4 is better and is not available yet

An ingester declaring "these kinds are reference data, do not expect them to be declared" is more precise than a global default, and it is where this likely ends up.
It needs an interface that does not exist, and it needs every plugin author to think about a question most will not.
The flag needs neither and is a strict improvement today, so it ships first.

This is the same reasoning [ADR-0038](0038-what-drift-may-say.md) used to prefer inference over an ingester-declared coverage list, and it lands in the same place: the cheap correct thing now, compatible with the precise thing later.

### One rule, one owner

The dangling note check moves out of `integrity` rather than being reported in both.
`integrity` keeps what it is for: the graph contradicting itself, through duplicate declarations of equal standing and relations pointing nowhere.
Drift takes what decays: claims reality stopped supporting.

Both reports would have used the same query, and two implementations of one rule is the failure this repository names explicitly.

The classification is imperfect and worth stating plainly.
SQL cannot tell "this ref was always a typo" from "this ref used to resolve".
Both surface in drift now, and a typo caught on the day it is written reads as decay.
This is accepted because the alternative is asking the operator which one it is before showing them either.

### The load-bearing rule

Drift stays silent about what is merely observed, unless asked.
It gets a named test: `TestADR0045_DriftIsSilentAboutWhatIsMerelyObserved`.

## Consequences

### Good

- The report opens with rows an operator can act on, in a length they will read.
- A note outliving its subject stops being invisible, which is the case that made this worth doing now.
- The observed direction is preserved exactly, one flag away, for documenting an estate.
- Nothing about ADR-0038's comparison logic changes, only which halves are returned by default.

### Bad

- First contact with a fresh catalog is worse. The undeclared list was an onboarding affordance, and now somebody has to know the flag exists to find it. The tool description carries that hint, which is a weaker place than the default output.
- An operator who genuinely wants to document everything running has to opt in every time.
- A note ref typo now reports as decay rather than as a graph problem, which is a slightly wrong label on a correct finding.
- `Drift` grows a parameter, so every caller and every test fake changed.
- Two reports that were adjacent are now one report and one smaller report, and somebody looking for the note check in `integrity` will not find it there.

### Rejected because

- **Option 1** was rejected because filtering per surface puts one decision in three places, which drift apart, and it leaves every API consumer holding the original problem.
- **Option 2** was rejected because a truncated list of things nobody will act on is still a list of things nobody will act on, and it is now also lying about its size.
- **Option 4** was rejected on sequencing rather than merit. An ingester declaring "these kinds are reference data" is more precise than a global default and is where this likely ends up. It needs an interface that does not exist and a question most plugin authors will not think about, and the flag needs neither.
- **Option 5** was rejected because "what is running here that I have not written down" is the right question on first contact with an estate, and it is how somebody documents one. It should be one flag away, not gone.

## Amendments

None yet.
