# Storage

The index is the materialized entity graph: SQLite, one file, partitioned by repository and git ref.

It is **derived and disposable**.
Deleting it loses nothing that reconciling from git cannot rebuild, which is what makes `AutoMigrate` safe here and a bad migration answerable by removing the file.

Settled in [ADR-0001](../adr/0001-git-as-source-of-truth.md) and [ADR-0008](../adr/0008-storage.md), implemented in `internal/index`.

## Two things are called a ref

This trips people up, so it is worth stating before anything else.

A **git ref** is a branch or pull request ref such as `refs/heads/main`. Together with the repository it partitions the whole index.
An **entity ref** is an entity's identity, `kind:namespace/name`, such as `service:home/jellyfin`.

In the schema they are the `git_ref` and `ref` columns.
In the Go API every method takes the git ref first and the entity ref second.

## Why it is keyed by git ref

Several refs are live at once.
That is what makes rendering a pull request as the catalog *as it would be after merge* nearly free: reconcile the unmerged ref into the same database and query it like any other.

Garbage collecting a closed pull request is then a delete scoped to its ref, with no per-ref lifecycle to manage and no second database to coordinate.

## Schema

| Table | What |
| --- | --- |
| `entities` | One row per entity, per repository, per git ref. Primary key is `(repository, git_ref, ref)` |
| `relations` | Typed edges. Primary key is `(repository, git_ref, from_ref, to_ref, type)` |
| `entity_fts` | FTS5 virtual table mirroring entity text for search |

Attributes are stored as protojson, so what comes back out is the same `structpb.Struct` that went in.

`entity_fts` is kept in step by SQLite triggers on `entities` rather than by explicit writes, so a second writer cannot forget to update it.

## Operations

| Call | What it does |
| --- | --- |
| `Put` | Replaces what one repository contributes at a git ref, in one transaction |
| `Get` | One entity, or `ErrNotFound` |
| `List` | Every entity at a git ref, optionally one kind |
| `Declared` | The refs one repository declares, which provenance cannot answer because it records the file and not the repository |
| `Search` | Full-text query, ranked, with a snippet |
| `Neighbors` | Every relation with an entity at either end |
| `Dependents` | Walks relations inbound, transitively, to a bounded depth |
| `DropGitRef` | Removes every repository's contents at a git ref |
| `DropRepository` | Removes one repository's contents at a git ref |
| `Scopes` | Which (repository, ref) partitions are materialized |
| `GitRefs` | Which refs are currently materialized |

`Put` **replaces** rather than merges, because git already gives the complete picture at a ref.
It is scoped to one repository so that a push to one does not require re-reading every other repository in the catalog.
It runs as one transaction, so a reconcile that fails partway leaves the previous contents rather than a half-built graph.

## Search

Search is FTS5, which is the single strongest reason the storage engine is SQLite.
Full-text search lives inside the database rather than in a service added later, and it brings ranking and `snippet()` with it.

Free text is turned into a query that cannot be a syntax error.
Each token is quoted as a phrase and the last is treated as a prefix, so results narrow as a query is typed and punctuation a user happens to type is searched for rather than interpreted.

Searches are scoped to a git ref and span every repository contributing to it, which is what makes the catalog searchable as one thing rather than per repository.

## Dependents

`Dependents` answers "what breaks if this goes away" by walking relations inbound with a recursive CTE, returning each entity with the length of the shortest path that reached it.

The depth bound is required, not a convenience.
A dependency graph can contain a cycle, and an unbounded walk on one would hang a request rather than fail it.

## No cgo

The driver is `github.com/glebarez/sqlite`, which is pure Go via `modernc.org/sqlite`.

This is load-bearing rather than incidental.
GORM's default SQLite driver requires cgo, which would cost cross-compilation, distroless images, and arm64 builds.
`make nocgo` is what keeps it from arriving again transitively.

## Rebuilding

There is no migration story and there does not need to be one.
Delete the file and reconcile.
