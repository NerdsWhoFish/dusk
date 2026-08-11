# 26. One file declares one entity, and its prose is the description

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

[ADR-0004](0004-dusk-md-convention.md) settled that a repo participates in the catalog if and only if it contains `dusk.md` at its root, and that Dusk reads that file and nothing else unless it explicitly points at other paths.
It deliberately said nothing about what is inside the file.

That gap has to close before the reconciler exists, because the reconciler's entire job is turning this file into the entities defined in [ADR-0007](0007-entity-schema.md).

The decision is close to permanent in the same way the `ref` format is.
Every repo that adopts Dusk authors against this shape, and every one of those files is owned by somebody who is not us.
A format change after adoption is a migration performed by strangers.

Four questions have to be answered together, because the answers constrain each other.

How many entities does one file declare?
Where does an entity's human-facing description come from?
How does a repo that owns thirty entities avoid a thirty-entity file?
And what stops a repo from declaring facts about entities it does not own?

## Considered Options

1. **One file, many entities.** Frontmatter carries an `entities:` list; the prose describes the repo.
2. **One file, one entity, plus a non-recursive `include:`** for repos that own more than one.
3. **Multi-document YAML**, Kubernetes style, with `---` separating one entity per document.

## Decision Outcome

Chosen: **option 2**.

A catalog file declares exactly one entity.
Its frontmatter carries that entity's identity and its declared relations, and **everything below the frontmatter is that entity's description**.

```markdown
---
dusk: v1alpha1
namespace: platform
kind: service
name: checkout
title: Checkout API
relations:
  - type: runs_on
    to: host:platform/runner-1
attributes:
  tier: "1"
include:
  - services/*/dusk.md
---

# Checkout API

Everything below the frontmatter is this entity's description.
```

Six rules give that shape its properties.

### The description is the prose, and there is no `description` field

An entity's description is the markdown body, not a quoted string in YAML.
Where a summary is needed rather than a full body, it is the first paragraph.

Two authorable sources for one field is how documentation rots, so there is exactly one.
This is also the property [ADR-0001](0001-git-as-source-of-truth.md) was protecting: metadata sits in the document it documents, and here the document *is* the metadata.

### `ref` is derived, never authored

The ref is computed as `kind:namespace/name` using the SDK's `conformance.CanonicalRef`.
A `ref` key in frontmatter is an error rather than an override.

Accepting both an authored ref and its component parts creates two representations of one identity, and the SDK's conformance check already exists to reject exactly that mismatch.
Deriving it means the mismatch cannot be authored in the first place.

### `namespace` is declared once and inherited

The root `dusk.md` must declare a namespace.
Included files inherit it and may override it.

Without inheritance every file in a thirty-entity repo repeats the same line, which is how the format acquires its first piece of noise.

### A file may only declare relations originating from its own entity

Relations are authored as `{type, to}`.
The `from` side is always the file's own entity and cannot be written.

This is load-bearing and is not merely a syntax saving.
It means a repo can never assert a fact about an entity it does not own, so `dusk.md` inherits git's existing trust boundary instead of needing a new one.
An edge that wants to point the other way is declared by the other repo, whose owners review it.

### `include` is honored only in the root `dusk.md`, and is not recursive

Globs are allowed; an included file's own `include` is an error.
Patterns are repository-relative and may not escape it: an absolute path or a `..` segment is rejected rather than resolved.

ADR-0004 rejected implicit scanning as invasive and unpredictable, and recursive includes reintroduce it by a slower path: an unbounded read surface reached one file at a time.
One level plus globs covers the monorepo case while keeping the cost of a reconcile something you can predict by reading a single file.

The escape rule matters more than it looks.
`include` is the one place a file under someone else's control names a path Dusk will then read, so it is the whole of the format's attack surface, and consent expressed by a file at a known location means nothing if that file can point anywhere on the host.

### This reader is the normalization edge for declared data

[ADR-0018](0018-normalization-at-the-edge.md) settled that plugins normalize and Dusk never re-derives, but it ruled on the ingest path: its subject is an `IngestBatch` and its actor is a plugin.
Nothing sits between a hand-written `dusk.md` and the graph, so on the declaration path there is no plugin to carry that obligation.

This ADR places it here.
The reader is to declarations what a plugin is to observations: what it returns is final, the reconciler validates, correlates and stores, and nothing downstream re-derives it.

That symmetry is stated rather than assumed because ADR-0018 rejected its option 3 for exactly the failure an unstated boundary produces, where "the boundary would move per plugin and per author until both sides did half the job".

It cuts both ways, and the second half is the one that constrains this parser hardest.
Normalizing is canonicalising what the author wrote; it is never inventing what they omitted.
A missing `kind` is an error, not a guess from the directory name, because [`docs/philosophy.md`](../docs/philosophy.md) forbids silent defaults and a wrong entry in a knowledge base outlives the convenience that produced it.

### Frontmatter is YAML, which costs the repository its first dependency

Dusk had no third-party dependencies before this decision.
That is a property worth defending, and the honest framing is that this ADR spends it.

YAML wins because it is what markdown frontmatter already *is*.
Jekyll, Hugo, Astro, and Obsidian all read this shape, so a `dusk.md` stays legible to the tools its author already runs, and Dusk gets to be a reader of an existing convention rather than the inventor of one more.

The parser is [`go.yaml.in/yaml/v3`](https://go.yaml.in/yaml/v3), the maintained continuation of `gopkg.in/yaml.v3`, which is archived.
It is pure Go, so [ADR-0017](0017-engineering-policy.md)'s cgo rule is untouched, and it exposes node positions, which is what makes an error able to name a line.

Rejected alternatives:

- **A hand-rolled YAML subset**, keeping the dependency count at zero. Rejected as the worst option available. Frontmatter is authored in repositories we do not control, so a subset parser meets anchors, block scalars, and quoting styles it does not implement, and mis-parses them into a catalog that is confidently wrong rather than failing.
- **JSON frontmatter**, using `encoding/json` and no dependency at all. Rejected because it is hostile to the humans expected to write it: no comments, no trailing commas, and quoting on every key, in a file whose entire pitch is that it documents itself.
- **TOML frontmatter.** Rejected because it needs a dependency too, so it pays the same cost while abandoning the convention compatibility that is YAML's actual argument.

### Unknown fields are rejected

Decoding is strict, and every error names the file, the field, and the expectation.

A silently ignored misspelling produces a catalog that is confidently wrong, which for this product is worse than one that fails to load.
[ADR-0007](0007-entity-schema.md) already lists typos fragmenting the taxonomy as a known cost of open vocabularies; strict decoding is where that cost gets paid down.

### What this file does not decide

**Notes have no home here yet.** [ADR-0007](0007-entity-schema.md) makes `Note` a first-class type and agents will write more notes than anything else, but a note is a markdown body with its own kind and lifetime, and cramming markdown bodies into a YAML list to ship sooner would be the wrong shape made permanent.
Entities and relations are what the reconciler needs; notes get their own decision, informed by the MCP write path in [ADR-0010](0010-mcp-surface.md) that will actually author them.

`dusk: v1alpha1` is required on every file so that this deferral, and any other later change, has somewhere to land.

## Consequences

### Good

- Prose has exactly one owner, so "which entity does this paragraph describe" is not a question the format can raise.
- Write routing is a file path. An agent editing an entity edits one file, which is what makes the per-call commits in [ADR-0010](0010-mcp-surface.md) land as clean, reviewable diffs instead of competing edits to one shared YAML list.
- Two agents editing two entities cannot conflict, because they are not in the same file.
- The trust boundary is inherited rather than invented. Declaring only your own outbound edges means catalog review is code review of the repo that owns the claim.
- Deriving the ref removes an entire class of correlation bug before anyone can author it.
- A bounded, predictable read survives contact with monorepos, which was a stated goal of ADR-0004 and the thing recursive includes would have quietly taken away.

### Bad

- A repo owning thirty entities has thirty files. Locality is the reason, but it is still thirty files, and an entity with nothing to say still needs one.
- Requiring `dusk: v1alpha1` is friction on a format whose pitch is that it is as simple as `CODEOWNERS`, which has no version field. It buys the ability to tell an old file from a new file that forgot, and that ambiguity is unfixable after adoption.
- Description-as-prose means an entity's description cannot be a single quoted line, which will annoy somebody declaring a trivial entity.
- Strict decoding makes the format less forgiving exactly when a repo is adopting it, which is when a hard failure is most discouraging.
- Non-recursive includes push monorepo authors toward glob patterns, and a wrong glob fails by finding nothing, which is quieter than an error.
- Only outbound relations means an entity's full neighbourhood is not visible in its own file; it is assembled by the reconciler across repos.
- The zero-dependency property is gone and does not come back. The next dependency is an easier argument than this one was, which is the real cost rather than the YAML parser itself.
- Depending on the plugin SDK to get the entity types pulls gRPC and `golang.org/x/net` in transitively, because the generated service code shares that module. Dusk needs gRPC eventually for Tier 2 plugins under [ADR-0002](0002-plugin-protocol.md), so this is early rather than wasted, but it arrived as a side effect rather than a choice.
- Declaring this reader the normalization edge means the rule now lives in two ADRs. Anyone changing the boundary has to find both.

### Rejected because

- **Option 1** was rejected because it leaves prose with no owner. One markdown body and N entities means descriptions can only come from YAML strings, which discards the property ADR-0001 exists to protect. It also makes every entity edit a modification to a shared list, so two agents touching two unrelated entities collide in one hunk, and it gives a repo a natural place to declare entities it has no claim to.
- **Option 3** was rejected because multi-document YAML is a YAML file wearing a markdown extension. Prose stops being first class, which contradicts the constraint in [DESIGN.md](../DESIGN.md) that config forced to document itself stays understandable. It inherits option 1's shared-file conflict problem without inheriting its single-file simplicity.
