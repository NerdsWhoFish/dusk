# Reconcile

Reconciling turns a repository at a git ref into the materialized graph.

It is the seam between the [`dusk.md` parser](dusk-md.md) and the [index](storage.md), and it is where [ADR-0004](../adr/0004-dusk-md-convention.md)'s promise is kept: the root file plus whatever that file explicitly points at, and nothing else in the repository is ever read.

Implemented in `internal/reconcile`.

## Reading and storing are separate

A **loader** reads a repository into a graph and touches no storage.
A **reconciler** stores what a loader read.

The split is what lets a local checkout be validated with no database, no server, and no network, while the server path uses exactly the same reading code.
Validation that ran different code from reconcile would answer a different question than the one being asked.

## What one reconcile does

1. Read `dusk.md` at the root. A repository without one has not opted in, which is not an error: anything previously stored at that ref is cleared and the reconcile reports it is not participating.
2. Parse it as the root file, which fixes the namespace every included file inherits.
3. Expand its `include` patterns, dropping duplicates and the root file itself, and sort the result so a reconcile is reproducible.
4. Parse each included file, collecting *every* problem rather than stopping at the first.
5. Reject two files declaring the same entity, naming both.
6. Replace everything the index holds for that ref, in one transaction.

A relation pointing at an entity that does not exist is **not** an error.
Entities live in the repositories that own them, so an edge routinely points across a repository boundary at something this reconcile cannot see.

## Sources

A `Source` reads files at a git ref, and it is the boundary [ADR-0005](../adr/0005-github-app-and-access-modes.md) requires: no GitHub type crosses it, so the reconciler is identical over a local directory and a remote repository.

`Dir` is the local implementation.
A directory has no refs, so it serves exactly one and refuses any other rather than quietly returning the same tree whatever it is asked for.

Reads go through `os.Root`, so a path leaving the directory fails at the filesystem rather than relying on the caller having sanitised it.
The parser rejecting `..` in an include pattern is the first line of defence; this is the second.

## Include patterns

Patterns are `path.Match` syntax.
`*` does not cross a directory separator and there is no `**`, so reaching deeper means naming another pattern.

```yaml
include:
  - services/*/dusk.md
  - datastores/*/dusk.md
```

## Validating locally

```console
$ dusk validate .
  dusk.md                     host:home/nas
  services/jellyfin/dusk.md   service:home/jellyfin
  services/navidrome/dusk.md  service:home/navidrome

. is valid: 3 entities, 2 relations, from 3 files
```

A repository with problems reports all of them and exits non-zero:

```console
$ dusk validate .
error: duskmd: services/jellyfin/dusk.md:3: field "kidn" is not a field this format defines
duskmd: services/jellyfin/dusk.md: field "kind" is required
duskmd: services/navidrome/dusk.md:6: field "relations[0].to" must be a ref of the form kind:namespace/name: ref "nas": missing kind prefix
```

`validate` reads exactly what a reconcile reads, so passing is a real answer to "will this repository load" rather than an approximation of one.

It deliberately does not write to an index or query one.
Reads belong to the API that the UI and the MCP server are ordinary clients of, and a second path into storage would be a third implementation to keep in step.
