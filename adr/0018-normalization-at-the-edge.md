# 18. Plugins normalize; Dusk never re-derives

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Every source describes the world differently.

Kubernetes has a Deployment in a namespace. Flux has a Kustomization pointing at a GitRepository. GitHub has a repository with an owner. Home Assistant has an entity with a domain prefix. A hand-written `dusk.md` has whatever its author typed.

All of them can describe the same service, and [ADR-0007](0007-entity-schema.md) requires a single canonical `ref` so that those descriptions correlate.

Somebody has to do that normalization: deciding the kind, canonicalising the ref, resolving the name, choosing which source field becomes which schema field.

There are only two places it can happen, and the choice compounds.
If normalization happens downstream, every renderer and consumer needs a branch per source, and adding either a source or an output multiplies the code paths.
That is an N sources by M outputs explosion, and it arrives gradually enough that nobody notices until it is expensive.

## Considered Options

1. **Normalize in Dusk**, with plugins emitting whatever their source natively looks like.
2. **Normalize in the plugin**, with Dusk receiving data already in final form.
3. **Split it**, with plugins doing obvious mappings and Dusk handling the rest.

## Decision Outcome

Chosen: **option 2**.

Every transformation from a source's model into the Dusk shape happens in the plugin.
By the time an `IngestBatch` reaches the graph it is final: `ref` canonical and consistent with its parts, `kind` resolved against the existing vocabulary, name normalized, source fields already mapped to schema fields.

**Dusk never re-derives what a plugin settled.**
The reconciler validates, correlates, and stores. Renderers read the common model and are blind to where the data came from.

Where a choice could plausibly live in either place, it belongs in the plugin.

The conformance package exists partly to enforce this: it rejects a batch whose `ref` disagrees with its `kind`, `namespace`, and `name`, so a plugin that skips normalization fails against its own test suite rather than against Dusk in production.

## Consequences

### Good

- The renderer is written once against the common model and never branches on source. A new output target is a renderer with no source-specific logic.
- A new source is one new plugin and nothing else changes.
- Source idiosyncrasies are absorbed at exactly one place per source, where the author already understands that source's quirks.
- Correlation works, because two plugins describing the same service both produce the canonical ref rather than two near-misses Dusk would have to reconcile heuristically.
- It keeps the graph honest as a model rather than a staging area of half-mapped source data.

### Bad

- It pushes real work onto plugin authors, which raises the cost of writing a plugin and is in tension with the goal that a plugin can be a shell script.
- A plugin that normalizes badly produces bad data that Dusk will faithfully store, since Dusk has deliberately given up the ability to correct it.
- Common normalization logic risks being reimplemented per plugin. The SDK should offer helpers, and those helpers become a de facto part of the contract.
- Changing a normalization rule means changing every plugin that implements it, rather than one place in the server.

### Rejected because

- Normalizing in Dusk was rejected because it requires the server to understand every source, which is precisely the coupling the plugin architecture exists to avoid. It also makes every new source a change to the server.
- Splitting it was rejected because an unclear boundary is worse than either side of it. "Obvious mappings in the plugin, the rest in Dusk" has no test, so the boundary would move per plugin and per author until both sides did half the job.
