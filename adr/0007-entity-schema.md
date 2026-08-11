# 7. A small entity schema with open kinds and an attributes escape hatch

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

The `.proto` established in [ADR-0002](0002-plugin-protocol.md) is the single contract every plugin compiles against.
It becomes a compatibility obligation the moment it is published, which makes this the least reversible decision in the project.

Two opposite failure modes bound it.

Modelling too much too early bakes in guesses.
Backstage's `Component`, `System`, `API`, `Resource`, `Group`, `Domain` taxonomy is simultaneously over-modelled for a single operator and under-modelled for the half of Dusk that answers "how do I do this again", which has no first-class representation there at all.

Modelling too little pushes everything into untyped blobs, which defeats the point of having a graph.

A related question arrived during design: when a Kubernetes ingester and a hand-written `dusk.md` both describe the same service, which one wins?

## Considered Options

For the type taxonomy:

1. **A distinct top-level message per kind**, in the Backstage style.
2. **A single `Entity` message carrying an open `kind` field.**

For source disagreement:

1. Last writer wins.
2. Declared beats ingested, or an explicit precedence field.
3. **Treat declaration and observation as different facets rather than competing claims.**

## Decision Outcome

### Four message types

- **Entity**: the thing. Carries an open `kind` field rather than having a message type per kind.
- **Relation**: a typed edge between two entities, such as `runs_on`, `depends_on`, `owns`, `documents`, `deploys_from`.
- **Note**: the "how do I do this again" unit. A gotcha, a runbook, a fact. Attaches to one or more entities. This is what agents write most, and it is the half Backstage has no answer for.
- **Observation**: an ingester-emitted status facet, carrying its source and timestamp.

### Four rules

1. **`kind` and relation types are open strings, not closed protobuf enums.** Adding a new kind must be data, not a schema change requiring a release.
2. **Every entity has a stable `ref`**, of the form `kind:namespace/name`. This is the correlation key, and it is the single hardest thing to change later.
3. **Every message carries `source` and `observed_at`.** Provenance is recorded, never inferred.
4. **An `attributes` map is the escape hatch.** Plugins put data the schema does not yet model there. When the same attribute appears across three independent plugins, it is promoted to a real field.

### Declaration and observation are different layers

There is no conflict resolution, because declarations and observations are not competing claims about the same field.

`dusk.md` **declares** intent: this service exists, it is owned by X, it depends on Y, it should run on Z.
An ingester **observes** reality: there is a Deployment named `foo`, three replicas, image `x`, on cluster `prod-2`.

This is the spec and status split from Kubernetes.
An entity carries both facets, and neither overwrites the other.

Divergence between them is surfaced as **drift**, which is a feature rather than a data-merging problem.
"Your `dusk.md` says this runs on prod-1, and the ingester found it on prod-2" is useful information, and silently merging it away would destroy the most valuable thing the two sources produce together.

### Versioning

Published as `v1alpha`.
No stability is promised until the three in-house ingesters (Kubernetes, Flux, GitHub) have shipped, per ADR-0002.

## Consequences

### Good

- Four types is small enough to hold in your head and small enough to implement correctly before the contract freezes.
- Open `kind` and relation strings mean the taxonomy grows without a release, which is what keeps early publication safe.
- The `attributes` map is the pressure valve. Without it the choice would be to over-model now or break compatibility later, and both are worse.
- `Note` as a first-class type is what makes Dusk answer "how do I do this again", which is half its reason to exist and is absent from every comparable catalog.
- The spec and status split turns the hardest open question into a feature. Drift detection falls out of the model rather than being built on top of it.
- Provenance on every message means "where did this claim come from" is always answerable, which matters a great deal once agents are writing.

### Bad

- Open strings mean typos become new kinds. This needs linting and a well-known-values list, or the taxonomy quietly fragments.
- The `attributes` map will be abused. Without discipline about promoting recurring attributes to real fields, the schema stays permanently thin and the data stays permanently untyped.
- A single `Entity` message with an open kind means kind-specific validation lives in application code rather than in the type system.
- The `ref` format is effectively permanent from first publication, and it is the thing most likely to be regretted.
- Carrying both declared and observed facets means every consumer must decide which it wants, and the UI has to present both without confusing anyone.

### Rejected because

- A message type per kind was rejected as over-modelling. It forces the taxonomy to be right before there is evidence, and every new kind becomes a schema change and a release.
- Last-writer-wins and precedence fields were both rejected because they answer the wrong question. They resolve a conflict that does not exist, and in doing so they discard the drift signal, which is the most useful product of having two sources describe the same thing.
