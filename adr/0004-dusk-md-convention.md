# 4. A repo opts in by containing `dusk.md`

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk needs to know which repos describe entities and where inside those repos to look.

Two failure modes bound the design.

Crawling every file in every accessible repo is invasive and unpredictable.
It surfaces things nobody meant to publish, it guesses wrong about what is an entity, and wrong guesses in a knowledge base are worse than missing data.

Requiring every entity to be hand-registered somewhere central produces a catalog that is stale on day one.
Nobody registers the boring services, which is precisely how Backstage catalogs decay.
It also creates a cold-start problem for anyone who already runs thirty services before installing Dusk.

## Considered Options

1. **Implicit scanning**: read frontmatter from every markdown file in every accessible repo.
2. **Central declaration**: all entities declared in the Dusk-tracked repo, other repos merely linked.
3. **Well-known file**: a repo participates if and only if it contains `dusk.md` at the root.

## Decision Outcome

Chosen: **option 3**.

A repo participates in the catalog if and only if `dusk.md` exists at its root.

`dusk.md` is the sole entry point.
Dusk reads that file and nothing else in the repo, unless `dusk.md` explicitly points at other paths.
Its frontmatter describes the repo and the entities it owns, and its prose is the human-facing description of the same.

This is a convention in the same family as `Dockerfile`, `CODEOWNERS`, and `.gitignore`.

### Consequences

#### Good

- No repo is ever crawled without consent. Consent is expressed by the existence of a file, which requires no central registration and no UI.
- Cold start is solved by installation. Granting access to twenty repos that already contain `dusk.md` populates the catalog immediately, with no migration step.
- The read surface is bounded and predictable. One file per repo, plus whatever that file explicitly opts into, which makes reconcile cost easy to reason about.
- Metadata lives beside the code it describes, preserving the property established in [ADR-0001](0001-git-as-source-of-truth.md).
- Write routing follows for free. An entity's home repo is wherever its `dusk.md` lives, so there is no routing table to maintain and no ambiguity about where an agent write belongs.

#### Bad

- A repo with no `dusk.md` is invisible, so the catalog is only as complete as adoption. Ingesters exist partly to cover this gap for infrastructure that has no repo of its own.
- One root file becomes a bottleneck for a monorepo that owns many entities. `dusk.md` pointing at other paths is the escape hatch, and that indirection needs to stay simple or it becomes a second config language.
- Checking for the file's existence still costs a lookup per repo per reconcile, which matters at large installation counts.

#### Rejected because

- Option 1 was rejected as invasive and unpredictable. It reads files nobody opted into publishing, and it infers entity-ness from shape rather than intent.
- Option 2 was rejected because it is stale by default and it breaks the locality established in ADR-0001. It also concentrates all writes into one repo, which removes the possibility of a service's own owners reviewing changes to their own catalog entry.
