# 83. Search fuses exact, full-text, and semantic retrieval

Date: 2026-08-25

## Status

Accepted. Supersedes [ADR-0082](0082-ai-search-uses-a-bounded-read-only-estate-agent.md) only for generic search retrieval and the estate agent's search tool.

The bounded tool loop, viewer visibility, read limits, audit trail, and read-only boundary in ADR-0082 still stand.

## Context and Problem Statement

FTS5 is fast and exact, but it cannot connect different vocabulary such as "tracing" and an OpenTelemetry-backed Grafana service unless one document happens to contain both terms.
That makes ordinary UI search and the AI estate agent miss useful catalog records for the same reason.

The catalog contains thousands rather than millions of documents, uses a pure-Go SQLite driver, and treats the whole index as disposable derived state.
Semantic retrieval must improve recall without making an embedding provider a dependency for writes or for keyword search.

## Considered Options

1. Fuse exact identity, FTS5, and semantic candidates while keeping vectors as ordinary SQLite rows
2. Replace FTS5 with vector-only search
3. Load a SQLite vector extension and use approximate nearest-neighbor search
4. Run a separate vector database
5. Keep FTS5 and add synonyms by hand

## Decision Outcome

Chosen: **option 1**.

Dusk keeps FTS5 as the immediate local search path, adds exact matching over refs, names, titles, and aliases, and optionally retrieves semantic candidates from an OpenAI-compatible embeddings endpoint.
Reciprocal rank fusion combines the independent rankings and preserves the rule that work notes rank below every other result.
Both the UI API and MCP use this one index query, so their results cannot drift.

Embeddings are stored as little-endian float vectors in ordinary SQLite BLOB columns and scored with cosine similarity in Go.
At the current catalog size a linear scan is simpler and fast enough, preserves the pure-Go build, and does not make a pre-1.0 extension or a database-driver replacement load-bearing.
The schema leaves the vectors behind the index package so storage can move to SQLite Vec1 later without changing callers.

Every vector records the source content hash and model.
A reconcile requests an incremental refresh after its transaction commits, an hourly sweep repairs missed work and backfills new models, and search accepts a vector only when its hash matches the current document.
An unavailable provider is logged and search falls back to exact plus FTS5 results.

The embedding model is deployment configuration rather than catalog truth.
Dusk supports any OpenAI-compatible endpoint, including a local service running an open source model, and never sends catalog text to one unless semantic search is explicitly configured.

## Consequences

### Good

- Queries can find relevant documents whose wording does not share a token with the query.
- UI search, MCP search, and the AI agent get the same retrieval improvement.
- Writes and FTS results remain immediate when the embedding service is unavailable.
- Content hashes prevent stale semantic results from surviving an update.
- SQLite remains the only durable index store and the build remains pure Go.
- Exact aliases and identifiers outrank probabilistic matches.

### Bad

- A search may make one network call to embed a query, adding latency when the query is not cached.
- The first backfill and a model change embed the whole catalog.
- Linear cosine scoring eventually stops scaling; a substantially larger catalog will need Vec1 or another indexed implementation.
- Relevance depends partly on the configured model, so two deployments can rank the same query differently.
- Embeddings add derived rows that consume more disk than FTS5 alone.

### Rejected because

- **Vector-only search** loses exact identifier behavior, deterministic prefix search, snippets, and graceful operation without a model.
- **A SQLite vector extension now** either introduces cgo or changes the database driver for a catalog small enough that a linear scan is cheap. That blast radius buys no user-visible capability today.
- **A separate vector database** creates another service, backup surface, visibility implementation, and consistency protocol for disposable data already owned by SQLite.
- **Hand-maintained synonyms** require the operator to predict every vocabulary mismatch and grow into a second taxonomy that silently drifts from the catalog.
