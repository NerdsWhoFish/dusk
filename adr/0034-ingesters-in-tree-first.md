# 34. Ingesters land in tree before the plugin protocol

Date: 2026-08-12

## Status

Accepted

## Context and Problem Statement

Dusk describes itself as a service catalog that maintains itself.
It does not yet maintain anything: every entity in a catalog exists because somebody typed a `dusk.md`.

The half that makes the claim true is ingestion, and it is entirely unbuilt.
[ADR-0011](0011-ingester-scheduling.md) settled how ingesters are scheduled, [ADR-0018](0018-normalization-at-the-edge.md) settled that they normalize at the edge, and [ADR-0002](0002-plugin-protocol.md) settled that plugins speak a subprocess protocol.
None of it has ever run.

The obvious reading of those decisions is that the first ingester should be a plugin, because that is the shape the architecture describes.
That reading builds two unproven things at once: the machinery an ingester needs, and the protocol by which a stranger's binary supplies it.

Two problems the machinery has to solve, neither of which the protocol helps with:

**Observed entities have no repository.** The index is partitioned by `(repository, git_ref)` and everything in it is traceable to a file somebody committed. A Kubernetes Service was not declared anywhere, so it needs a scope that means "seen, not written".

**Failure must never look like absence.** [ADR-0011](0011-ingester-scheduling.md) is unambiguous, and it is the rule most easily broken by a natural implementation: replacing an ingester's contents on each run deletes everything the moment a cluster is briefly unreachable.

## Considered Options

1. **Plugin protocol first.** Build [ADR-0002](0002-plugin-protocol.md)'s subprocess contract, then write the Kubernetes ingester against it as its first consumer.
2. **In tree first.** Build the ingestion machinery with an interface, and one in-tree Kubernetes ingester as its first consumer. The plugin host becomes a second implementation of that interface later.
3. **Only ever in tree.** No plugin protocol; every ingester is compiled in.

## Decision Outcome

Chosen: **option 2**.

`internal/ingest` owns the machinery: an `Ingester` interface, a scheduler applying [ADR-0011](0011-ingester-scheduling.md)'s rules, and a merge into the index that preserves prior observations on failure.
The Kubernetes ingester implements that interface in tree.

### The protocol is the easy half, and it is second

An out-of-process plugin is a different way to *deliver* entities.
It changes nothing about when an ingester runs, what happens when it fails, how observations are scoped, or how a stale entity is recognised, and those are the parts that are hard and easy to get wrong.

Building the protocol first means the first time the never-delete rule is exercised, it is exercised through a gRPC boundary and a subprocess lifecycle that are themselves new.
A failure then has three candidate causes.

With the interface proven against a real cluster, the plugin host is a second implementation of something whose behaviour is already pinned by tests.

### Observation is a scope, not a repository

An ingester writes into the index under a reserved scope, `ingester:<name>` in place of a repository, at the ref `observed`.

This falls out of what already exists rather than adding a concept.
The index is partitioned by `(repository, git_ref)`, `DropRepository` already scopes a removal, and [ADR-0008](0008-storage.md)'s guarantee that the index is disposable holds unchanged: observations are re-derived by running the ingester again, exactly as declarations are re-derived from git.

It also makes the declared and observed halves comparable, which is what [ADR-0013](0013-layout-and-pages.md)'s drift block needs and what makes ingestion worth having at all.

### Merging never deletes on failure

An ingester run either succeeds and replaces its scope wholesale, or fails and changes nothing.

There is no partial application. A run that returns an error leaves the previous observation in place, and the entities keep the `observed_at` they were last seen with, so staleness is derived rather than tracked separately, exactly as [ADR-0011](0011-ingester-scheduling.md) requires.

This is the load-bearing rule and it gets a named test.

### Declared beats observed

Where a ref exists in both a repository and an ingester's scope, the declared one wins for reads.

A human wrote it deliberately and an ingester inferred it. The observed copy is not discarded, because the comparison between them is drift, which is the point.

## Consequences

### Good

- The claim in the tagline becomes true, against real infrastructure, without a protocol in the way.
- The rule most likely to destroy a catalog is exercised early, in-process, where a test can force the failure directly.
- Drift becomes possible, because declared and observed are both in the index and comparable by ref.
- The plugin host arrives later as an implementation of a proven interface, which is a smaller and better specified job than inventing both together.
- An operator gets value with no plugin to install, which matters most at first contact.

### Bad

- **The Kubernetes client is now a dependency of the main binary**, and it is a heavy one. Every operator carries it whether or not they run a cluster. This is the clearest cost, and the strongest argument for the plugin boundary existing eventually.
- Compiled-in ingesters cannot be added without a release, so the set is whatever shipped.
- Two ways to get entities into the index now exist, and the second one is not yet the pluggable one. Until the plugin host lands, "how do I add an ingester" is answered by "send a pull request".
- A reserved scope shaped like a repository will read as a repository somewhere it should not, most likely in a UI or a status list that assumes it can be cloned.

### Rejected because

- **Option 1** was rejected on sequencing rather than merit. It is where this ends up. Doing it first means the never-delete rule and the subprocess contract are both new at the same moment, and a bug in either presents identically.
- **Option 3** was rejected because [ADR-0002](0002-plugin-protocol.md) already settled that a stranger should be able to extend Dusk without forking it, and because a catalog whose sources are fixed at compile time cannot describe an estate its author did not anticipate.

## Amendments

### 2026-08-13: the sequencing finished, and the bet paid

This ADR was explicit that in-tree was a stage rather than a destination. That stage is over: the Kubernetes ingester now lives in `dusk-plugin-kubernetes`, and core carries none ([ADR-0040](0040-core-and-plugins.md)).

Three of the Bad consequences above expired with it. `k8s.io/client-go` is out of the binary. Ingesters no longer need a release to add. There are no longer two ways to get entities into the index.

The remaining one did not expire: a reserved scope still looks like a repository to anything that does not check, which is exactly how it bit later. Removing an in-tree ingester renames its scope, and the orphaned observations under the old name read as a second declaration of every ref. Nothing was watching for a scope nothing refreshes.

The bet itself was right, and worth recording because the cheaper path was so obviously available. Building the machinery in-process meant the never-delete rule, the completeness contract and the scheduler were all proven against real infrastructure before a subprocess and a wire format existed to confuse the diagnosis. When the plugin host arrived it implemented an interface that already worked, and every bug found in that work was a plugin bug rather than an ambiguity about what an ingester is.
