# 8. SQLite for the materialized graph, via GORM on a pure-Go driver

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

The entity graph is a **derived** view, rebuildable from git at any time, as established in [ADR-0001](0001-git-as-source-of-truth.md).
That makes this decision unusually reversible, which is worth stating up front: getting it wrong costs a rewrite of one layer, not a data migration.

The requirements are specific.

Several git refs must be live at once, because pull request previews render the catalog at an unmerged ref.
Closing a pull request must garbage-collect that ref cheaply.
Relation traversal must be fast enough to answer "what depends on this" interactively.
Full-text search is not optional, because search is the product for the half of Dusk that answers "where is this thing".
And Dusk ships as a single binary and a multi-architecture container, so anything requiring an external service or a C toolchain is expensive.

## Considered Options

1. **In-memory graph**, rebuilt on start.
2. **Embedded key-value store** such as bbolt, badger, or pebble.
3. **SQLite**, embedded.
4. **PostgreSQL.**
5. **A graph database** such as Neo4j or Dgraph.

## Decision Outcome

Chosen: **SQLite**, accessed through **GORM** using the **`github.com/glebarez/sqlite`** driver.

- One database file, with `ref` as a column rather than a database per ref.
- WAL mode.
- Garbage collection on pull request close is `DELETE WHERE ref = ?`.
- Relation traversal uses recursive common table expressions, written as raw SQL.
- Full-text search uses FTS5, also raw SQL.
- Schema is managed with GORM `AutoMigrate`, which is low-risk here because the database is disposable.

The driver choice is load-bearing and is not interchangeable with the default.
`gorm.io/driver/sqlite` wraps `mattn/go-sqlite3` and **requires cgo**, which would break cross-compilation, distroless and Alpine images without a C toolchain, and clean `go test -race`.
`github.com/glebarez/sqlite` is GORM over `modernc.org/sqlite`, is pure Go, and is the GORM project's recommended CGO-free driver.

## Consequences

### Good

- Multiple live refs are a column, not an architecture. No coordination between databases and no per-ref lifecycle to manage.
- Garbage collection is a single delete statement.
- FTS5 provides full-text search inside the storage layer, so search does not require a separate service later. Given that search is a core product requirement, getting it for free here is the strongest single argument for SQLite.
- The single-binary story survives. Pure Go means no cgo, no C toolchain in CI, and `GOOS`/`GOARCH` cross-compilation that simply works, which matters directly for arm64 builds.
- The database is inspectable with the `sqlite3` CLI when something is wrong, which is worth more during an incident than any abstraction.
- `AutoMigrate` is genuinely useful here precisely because the store is disposable. A migration that goes wrong is answered by deleting the file and reconciling again.

### Bad

- SQLite has single-writer semantics. This is acceptable because writes are reconcile-driven and serialize through one writer, but it forecloses multiple Dusk instances behind a load balancer without revisiting this decision.
- The pure-Go driver is measurably slower than the cgo one. The tradeoff is accepted deliberately, and the workload is not write-heavy.
- GORM adds the least value in exactly this case. The two hardest queries, recursive traversal and FTS5, are raw SQL regardless, leaving GORM responsible for `AutoMigrate` and CRUD across a handful of tables. It is used because it is familiar and the blast radius is near zero, not because it is carrying much weight.
- Reflection-based ORMs obscure the generated SQL, which makes query performance harder to reason about than hand-written SQL or a code generator such as `sqlc` would be.

### Rejected because

- An in-memory graph was rejected on restart cost and memory. Rebuilding every ref on every start scales badly with repo count, and several live refs multiply the footprint.
- Embedded key-value stores were rejected because they solve persistence and nothing else. Indexes, relation traversal, and full-text search would all be hand-built, which is a large amount of work to reproduce what SQLite already ships.
- PostgreSQL was rejected because it requires an external service for what is explicitly a disposable cache, and that destroys the run-it-anywhere property for no benefit at this scale.
- Graph databases were rejected as disproportionate. The workload is thousands of entities, not millions, and the operational weight is not remotely justified.
