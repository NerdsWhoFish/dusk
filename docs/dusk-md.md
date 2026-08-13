# The `dusk.md` format

A repository joins the catalog by containing a `dusk.md` at its root.
Nothing else opts it in, and nothing else is read without that file pointing at it.

The rules below are settled in [ADR-0004](../adr/0004-dusk-md-convention.md) and [ADR-0026](../adr/0026-dusk-md-schema.md), and implemented in [`pkg/duskmd`](../pkg/duskmd).

## The shape

A catalog file declares exactly one entity.
Its frontmatter carries identity and outbound relations, and everything below the frontmatter is that entity's description.

```markdown
---
dusk: v1alpha1
namespace: home
kind: service
name: jellyfin
title: Jellyfin
relations:
  - type: runs_on
    to: host:home/nas
  - type: depends_on
    to: datastore:home/postgres
attributes:
  backup: nightly
include:
  - services/*/dusk.md
---

# Jellyfin

Media server. Transcoding is off, so anything that will not direct play is a client problem.

Everything here is the description, markdown and all.
```

## Fields

| Field | Required | What it is |
| --- | --- | --- |
| `dusk` | yes | Schema version. `v1alpha1` is the only accepted value |
| `kind` | yes | What sort of thing this is. Open vocabulary: `service`, `host`, `datastore`, or anything else you need |
| `name` | yes | The entity's name within its namespace |
| `namespace` | on the root file | Groups entities. Declared once on the root `dusk.md` and inherited by included files, which may override it |
| `title` | no | Human-facing name. Defaults to `name` |
| `relations` | no | Edges originating from this entity. Each is a `type` and a `to` |
| `attributes` | no | Anything the schema does not model yet. Values must be representable in JSON |
| `observed_as` | no | Refs an ingester uses for this same thing, so drift matches them up |
| `include` | root file only | Glob patterns naming further catalog files |

Any other field is an error.
A misspelling that was quietly ignored would produce a catalog that is confidently wrong, which is worse than one that refuses to load.

**Do not repeat the title as a heading in the prose.**
`title` is already the entity's name, and markdownlint's MD025 treats a frontmatter `title` as the document's top-level heading, so a body opening with `# Checkout API` is reported as a second one.
Start the prose with the prose.

## What you do not write

**The ref.** Every entity has a stable ref of the form `kind:namespace/name`, and it is computed from the three fields above.
Declaring `ref` is an error rather than an override, because two representations of one identity can only drift apart.

**The description.** It is the prose, not a field.
Writing `description` in frontmatter is an error.

**A relation's `from`.** It is always the entity declared by the file you are editing.
An edge in the other direction is declared by the repository that owns the other end, so a repository can never assert a fact about an entity it does not own.

## Owning more than one entity

The root `dusk.md` may point at other files with `include`, and each of those declares one more entity in exactly the same way.

```yaml
include:
  - services/*/dusk.md
  - datastores/*/dusk.md
```

Three limits apply.

Includes are **one level deep**: an included file may not include further files.
Patterns are **repository-relative**: an absolute path or a `..` segment is rejected rather than resolved.
Included files **inherit the root's namespace** unless they declare their own.

The depth limit exists because recursive includes would turn a bounded read into an unbounded crawl of your repository, one file at a time, which is exactly what the `dusk.md` convention exists to avoid.

## Errors

Every violation names the file, the line, the field, and what was expected.

```text
duskmd: services/api/dusk.md:4: field "kidn" is not a field this format defines
duskmd: services/api/dusk.md:2: field "dusk" must be "v1alpha1", got "v2"
```

A file is validated in full rather than abandoned at the first problem, so one parse reports everything wrong with it.

## Declared, not observed

A `dusk.md` says what you intend: this service exists, it should run on that host, it depends on that database.
Plugins report what is actually there.

Neither overwrites the other, and disagreement between them is surfaced as drift rather than merged away.
"Your `dusk.md` says this runs on the NAS, and the ingester found it on the Pi" is information worth having, so the catalog keeps both.
See [ADR-0007](../adr/0007-entity-schema.md).

Where both exist for the same ref, the declared one wins a read: a person wrote it deliberately and an ingester inferred it ([ADR-0034](../adr/0034-ingesters-in-tree-first.md)).

### Saying which observed thing is yours

A human and an ingester never independently pick the same name.
You write `service:home/jellyfin`; a Kubernetes plugin finds a Service in a namespace and calls it `service:prod/media-jellyfin`.

`observed_as` is how you say they are the same thing:

```yaml
---
dusk: v1alpha1
kind: service
name: jellyfin
observed_as:
  - service:prod/media-jellyfin
---
```

Without it, drift reports your declaration as missing and the observed service as undeclared, forever, and the report is noise rather than a signal.

An `observed_as` naming something no ingester sees is still reported.
The mapping is a claim, and a claim that does not hold is exactly what drift is for.
