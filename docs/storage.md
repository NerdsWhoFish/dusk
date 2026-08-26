# Storage

The index is the materialized entity graph: SQLite, one file, partitioned by repository and git ref.

It is **derived and disposable**.
Deleting it loses nothing that reconciling from git cannot rebuild, which is what makes `AutoMigrate` safe here and a bad migration answerable by removing the file.

`actions.json` beside it is deliberately different.
That bounded journal holds recent action events, idempotency receipts, and asynchronous handle links, none of which Git can rebuild.
Deleting it can make Dusk forget that a mutation already ran, so it belongs in backups and a malformed journal stops startup rather than being discarded ([ADR-0070](../adr/0070-mutations-have-durable-retry-identity.md)).

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
| `catalog_fts` | FTS5 virtual table mirroring entity and note text, so one search ranks both |
| `embedding_rows` | Optional model- and content-hash-specific vectors derived from the same entities and notes |

Attributes are stored as protojson, so what comes back out is the same `structpb.Struct` that went in.

`catalog_fts` is kept in step by SQLite triggers rather than by explicit writes, so a second writer cannot forget to update it.

## Operations

| Call | What it does |
| --- | --- |
| `Put` | Replaces what one repository contributes at a git ref, in one transaction |
| `Get` | One entity, or `ErrNotFound` |
| `List` | Every entity at a git ref, optionally one kind |
| `Declared` | The refs one repository declares, which provenance cannot answer because it records the file and not the repository |
| `Search` | Exact identity, full-text, and optional semantic candidates fused into one ranked page |
| `SimilarNotes` | Notes that nearly say a given body already, scored and ordered |
| `Neighbors` | Every relation with an entity at either end |
| `Dependents` | Walks relations inbound, transitively, to a bounded depth |
| `DropGitRef` | Removes every repository's contents at a git ref |
| `DropRepository` | Removes one repository's contents at a git ref |
| `Scopes` | Which (repository, ref) partitions are materialized |
| `LastRead` | When each partition in the default view was last read, from the `observed_at` stored with its rows |
| `GitRefs` | Which refs are currently materialized |

`Put` **replaces** rather than merges, because git already gives the complete picture at a ref.
It is scoped to one repository so that a push to one does not require re-reading every other repository in the catalog.
It runs as one transaction, so a reconcile that fails partway leaves the previous contents rather than a half-built graph.

## Search

Search always starts with exact identity and FTS5, which is the single strongest reason the storage engine is SQLite.
Full-text search lives inside the database rather than in a service added later, and it brings ranking and `snippet()` with it.

Free text is turned into a query that cannot be a syntax error.
Each token is quoted as a phrase and the last is treated as a prefix, so results narrow as a query is typed and punctuation a user happens to type is searched for rather than interpreted.

Searches are scoped to a git ref and span every repository contributing to it, which is what makes the catalog searchable as one thing rather than per repository.

When an embeddings endpoint is configured, Dusk also embeds entity and note text and stores the vectors as ordinary SQLite BLOBs.
Cosine similarity is calculated in Go and reciprocal rank fusion combines semantic candidates with exact and FTS ranks.
The catalog size is deliberately small enough that a linear vector scan avoids cgo, a database-driver replacement, and another service owning index state.

Each vector records its model and source content hash.
A reconcile signals a refresh after committing, an hourly sweep repairs missed work, and a vector whose hash no longer matches is excluded immediately.
If embedding a query fails, exact and FTS search still answer.
This lifecycle and the rejected vector-extension trade-off are recorded in [ADR-0083](../adr/0083-search-fuses-exact-full-text-and-semantic-retrieval.md).

## Notes that nearly say the same thing

`SimilarNotes` is what stops the catalog accumulating the same knowledge twice ([ADR-0053](../adr/0053-note-dedup.md)).

It is two mechanisms because one will not do.
FTS5 matches **any** of the body's words rather than all of them, which is what keeps a reworded note a candidate, and ranks them, so the catalog is narrowed to fifty rows for the cost of one query.
The score is then counted in Go as the share of vocabulary the two have in common, because bm25 rank is relative to its query and no fixed threshold can be written against it.

Words of two characters or fewer are ignored. There is no stemming and no stopword list, so this finds a copy and a light edit, not a paraphrase.

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
