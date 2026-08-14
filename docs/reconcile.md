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

## Every read is pinned to one commit

A ref is not a fixed thing.
`refs/heads/main` means a different tree after every push, so a reconcile that resolved the ref separately for each file could assemble a graph from two commits, silently.

So a `Source` resolves the ref to a commit **once**, and every read in that reconcile is made against the commit.
The commit is recorded as provenance, so a claim in the catalog traces to the exact tree that produced it.

This is also why the GitHub source can cache a tree listing with no invalidation logic: a commit's tree cannot change.
Reasoning in [ADR-0029](../adr/0029-reading-repositories.md).

## Sources

A `Source` is the boundary [ADR-0005](../adr/0005-github-app-and-access-modes.md) requires: no GitHub type crosses it, so the reconciler is identical over a local directory and a remote repository.

It has exactly two jobs.
`Resolve` turns a ref into a commit, and `Tree` returns the catalog files at that commit as a `catalogfs.Tree`.

**Everything else is on the tree, not on the source.**
Matching an include pattern, reading a file, and deciding what counts as a catalog file all live in [`pkg/catalogfs`](packages.md), shared by every reader.
This is not tidiness: three readers once carried their own matching, two grew `**` and a markdown filter while the third grew neither, and the result was `dusk validate` resolving an include differently from the server.
A source that only produces a tree cannot drift from the spec.

**`reconcile.Dir`** is the local implementation, for a checkout.
A directory has no refs, so it serves exactly one and refuses any other rather than quietly returning the same tree whatever it is asked for.
It has no commits either, so `Resolve` returns the ref name in place of one.

It walks through `os.Root`, so a path leaving the directory fails at the filesystem rather than relying on the caller having sanitised it.
The parser rejecting `..` in an include pattern is the first line of defence; this is the second.
Because the tree only ever holds paths the walk produced, an escaping path cannot be expressed at all rather than being caught at read time.

**`reconcile.Tarball`** is the remote implementation ([ADR-0032](../adr/0032-tarball-reads.md)).
It probes for a root `dusk.md` in one request and downloads the tree only if the repository has opted in, so the majority that hold nothing for Dusk are never transferred.
Both the resolve and the download are memoized, so the controller and the loader asking the same question cost one call rather than two.

## Include patterns

Patterns support `*`, which does not cross a directory separator, and `**`, which matches any number of directories including none.
`docs/**/*.md` therefore matches `docs/a.md` as well as `docs/deep/b.md`, because requiring an intermediate directory surprises everybody who writes it.

Only markdown is matchable.
A tree holds nothing else, so a pattern cannot reach a repository's source or build output whatever it says.
Git's own directory is excluded too, since a packfile can hold a name ending in `.md` and is never catalog content.

`.dusk/` is read whether or not an include reaches it, and two paths inside it are skipped: `.dusk/home.md` and `.dusk/kinds.md` are Dusk's own declared configuration rather than catalog content, and are read by the code that declared them ([ADR-0048](../adr/0048-the-kind-vocabulary.md)).
`catalogfs.IsReserved` is the one place that list lives.
The vocabulary file is parsed here anyway, so `dusk validate` catches a bad role locally rather than on the server.

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
  .dusk/transcoding.md        note/gotcha
  .dusk/kinds.md              mints entity/airport as reference

. is valid: 3 entities, 2 relations, 1 notes, 1 minted kinds, from 4 files
```

Notes are listed alongside entities, because a repository whose notes all failed to parse would otherwise report clean here and fail on the server.
Minted kinds are listed for the same reason.

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
