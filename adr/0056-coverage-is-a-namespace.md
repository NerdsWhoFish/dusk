# 56. Coverage is a namespace, and a row is not a verdict

Date: 2026-08-15

## Status

Accepted

Supersedes [ADR-0038](0038-what-drift-may-say.md).

## Context and Problem Statement

[ADR-0038](0038-what-drift-may-say.md) settled that absence is only evidence where there is a witness, and derived the witness from observations: a kind is watched if some ingester observed at least one entity of that kind.
It recorded the granularity as a known cost, in its own words, "a kind is global, so an ingester covering one cluster makes `service` watched everywhere".
That was written as a refinement somebody would get to later.

Driven against a real catalog it is not a refinement.
It is the whole feature.

Every row in the declared half was wrong.
Twenty one of twenty one, which is a false positive rate of one hundred percent, and the rows were the core of the catalog: the hosts, the services, the network and the devices an operator had written down by hand.
Among them was the host the session doing the reading was running on.

The cause is that the two halves of the catalog never name things in the same namespace and never will.
A person writes `host:estate/nas`, choosing a namespace for the estate they are describing.
A plugin normalizes at the edge ([ADR-0018](0018-normalization-at-the-edge.md)) and names what it found after the thing it read: a Kubernetes instance emits `host:cluster-a/node-1`, a network appliance emits `device:appliance/<mac>`.
So the moment any plugin observed one `host` anywhere, every hand-written `host` in the estate became a declaration nothing could find, permanently, with no action that would ever clear it.

A report that is entirely noise is worse than no report, because it is read once by somebody deciding whether to trust the catalog at all.
This one was worse still, because of what it said about the rows.
The renderer's closing line was "Either these are gone and their declarations should be removed", so an agent acting on the report as written would have proposed deleting the catalog's contents.
Drift knew only that nothing had reported these; it asserted that they were gone.

Two smaller failures were found in the same pass, both of the same family: the report saying more than it knows, or less than it could.

`observed_as` is the mechanism that fixes the commonest real cause of a row, a human and an ingester naming one thing differently.
It is documented in [`docs/dusk-md.md`](../docs/dusk-md.md) and drift has never mentioned it, so the fix is invisible at the moment somebody is reading the problem.

And drift's advice on an orphaned note is to close it with status done or dropped.
The query behind that section had no status predicate, so closing one changed nothing and the row came back.
A queue that ignores its own instructions cannot be emptied.

## Considered Options

1. **Leave coverage at the kind and accept the noise.** ADR-0038 as it stands, with the report filtered by whoever reads it.
2. **Drop the declared half entirely.** If coverage cannot be inferred well enough to be trusted, report only what is observed and undeclared, and say the other question cannot be answered.
3. **Narrow the inference to the namespace.** A kind is watched in the namespaces it was observed in, and nowhere else.
4. **Have ingesters declare their coverage.** ADR-0038's own option 4: each instance states what it is responsible for, and drift judges exactly that.
5. **Configure coverage per deployment.** An operator lists which kinds and namespaces drift may judge.

## Decision Outcome

Chosen: **option 3**, with an `observed_as` counted as a witness in its own right.

A declared entity is reported only when something observed its kind **in its namespace**, or its own declaration names an `observed_as`.

This is the load-bearing rule and it gets a named test: `TestADR0056_CoverageIsPerNamespaceNotPerKind`.
The second half gets `TestADR0056_AnObservedAsIsItsOwnWitness`.

### A namespace is the scope the ref already carries

Every ref is `kind:namespace/name` ([ADR-0007](0007-entity-schema.md)), so the namespace is the only scoping coordinate present on both sides of the comparison without inventing anything.
It is also, in practice, exactly the axis ADR-0038 named when it said one instance covering one cluster should not make a kind watched everywhere, because a plugin observing a cluster puts that cluster in the namespace.

This keeps the property ADR-0038 chose option 3 for over option 4: coverage is derived, so an operator adding an ingester gets drift coverage for what it observes with nothing to configure, and there is no list to go stale in silence.
It changes only how finely the derivation is read.

### An `observed_as` is the operator supplying the witness

Narrowing coverage would otherwise have silenced the one case the alias exists for.
Writing `observed_as` is the operator stating that an ingester sees this thing under another name, which is precisely the claim the coverage rule is looking for, made by hand instead of inferred.

So a declaration carrying an alias is judged wherever it sits.
If the alias resolves, the row clears, which is the mechanism working.
If it does not, the row stays, because [`docs/dusk-md.md`](../docs/dusk-md.md) already promises that "an `observed_as` naming something no ingester sees is still reported", and a claim that does not hold is what drift is for.

The two mechanisms compose into one sentence: drift judges a declaration when something is watching where it lives, or when its author said something is.

### The framing is the feature, not decoration

A row means something watched and did not report this.
It does not mean the thing is gone, and the report may not say that it does.

The declared section now states what it can be: gone, known to an ingester by another name, or sitting behind a filter its ingester does not cross, with only the first being a declaration to remove.
It then names `observed_as` and where to find the ref an ingester is using, in the same shape the note section already used well.
The tool description and the server instructions carry the same limit, because an agent reads those before it ever sees a row.

This is [`docs/philosophy.md`](../docs/philosophy.md)'s rule that anomalies are surfaced and never silenced, applied to the report's own certainty.
Surfacing a finding as though it were a verdict is a way of silencing what is not known about it.

### Closing an orphaned note clears it

The note section keeps its advice and the query now honours it: only a note nobody has closed is reported.
Empty status stays open, so nothing written before there was a status reads as finished, which is the rule the note schema already carries everywhere else.

The predicate is shared with `Notes` rather than spelled a second time, because two implementations of one rule is a failure this repository names explicitly.

### Option 4 is still the destination

An ingester declaring "I cover services in this cluster" remains more precise than inferring it from what came back, and it is still where this ends up.
It still needs an interface that does not exist, in a repository this change does not touch.

The difference from ADR-0038 is that the cheap correct thing is now measurably correct rather than merely better, and the gap option 4 would close is smaller than the gap it left.

## Consequences

### Good

- The declared half is usable. On the catalog that produced twenty one false positives it produces none, because no ingester observes anything in the namespace those declarations use.
- The rule stays derived, with nothing to configure and nothing to keep in step with what is deployed.
- A naming mismatch now names its own fix where somebody is reading the problem, instead of in a document they would have to already know about.
- Closing an orphaned note empties it from the queue, so the section's advice is worth following.
- An agent can act on the report. The old copy invited deleting declarations that were fine, which is the failure [ADR-0017](0017-engineering-policy.md) names as the only one that matters: the catalog being confidently wrong.

### Bad

- **Losing every ingester for a namespace makes drift go quiet about it rather than loud.** This is ADR-0038's headline cost made finer and therefore larger: previously one cluster's ingester kept a kind watched everywhere, and now the ingester for cluster A going away stops cluster A's declarations being checked while the rest of the estate reports normally. Plugin health has to be watched through `changes`, which is where a failing instance already surfaces, and drift shrinking is not an alarm.
- **An estate whose declarations all sit in one namespace nothing observes now gets an empty declared half, and that will read as a broken feature.** It is the correct answer to the question, and it is indistinguishable from the report never running. The same shape is already recorded for a restricted viewer in [ADR-0051](0051-a-count-is-of-what-the-viewer-can-see.md).
- **Namespace is being asked to carry the meaning of scope, and nothing enforces that.** It holds because plugins normalize at the edge and name what they read, not because any contract says so. A plugin that emits one namespace across two clusters gets no benefit from this rule, and a plugin that puts every entity in a namespace of its own makes the declared half silent for everything it touches.
- A closed note pointing at nothing is no longer visible in drift. It is still returned by `note`, but somebody looking for the orphan in the report will not find it after closing it, which is the point and will still surprise.
- The report now says more per section. It is longer to read, and the length is spent on caveats rather than rows.

### Rejected because

- **Option 1** was rejected by measurement. One hundred percent of the rows were wrong, so there was nothing for a reader to filter down to, and the section actively argued for deleting good declarations.
- **Option 2** was rejected because the question is answerable, just not at the granularity ADR-0038 asked it. Dropping the half would also lose the case that works today: a declaration inside a namespace something does watch, which is a real "this is gone" and is what the queue is for.
- **Option 4** was rejected on sequencing again, and for the third time it is recorded as the intended destination rather than a worse idea. It needs a change to the plugin contract in another repository, and the namespace inference gets the measured benefit now.
- **Option 5** was rejected for the reason ADR-0038 rejected its equivalent: a configured list fails silently in both directions and asks an operator to maintain what Dusk can derive.

## Amendments

None yet.
