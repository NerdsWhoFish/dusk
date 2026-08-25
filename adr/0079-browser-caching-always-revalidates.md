# 79. Browser caching paints immediately and always revalidates

Date: 2026-08-25

## Status

Accepted

## Context and Problem Statement

The live default homepage took roughly 3.3 seconds to answer with a catalog of about two thousand entities.
The largest cause was not transfer size: the `reads` block loaded every entity once per materialized source, producing dozens of redundant SQLite reads before the page could paint.
Live verification after that fix isolated another 2.8 seconds in drift's correlated declared-versus-observed comparison.
Its predicates searched by observed ref, watched kind and namespace, and both directions of an alias while the available indexes began with repository, so SQLite repeatedly scanned the wrong shape.

After fixing that query, repeat navigation still paid for reads the browser had already received.
The operator wants fast revisits without a cache that quietly hides a catalog change, because a stale answer presented as current violates Dusk's anomaly posture.

## Considered Options

1. Add a time-based server response cache.
2. Rely on ordinary HTTP freshness caching.
3. Keep no browser cache and preload likely destinations.
4. Keep a browser-session answer, paint it immediately, and always revalidate it.

## Decision Outcome

First remove the avoidable work.
The index now answers entity counts for every materialized scope in one aggregate query, and the `reads` block consumes that result instead of loading full entities once per scope.
Composite indexes follow drift's observed-ref, observed-kind-namespace, ref-alias, and alias-ref predicates so the comparison can seek instead of rescan.

For repeat reads, keep home, graph, vocabulary, entity, and note responses in memory and `sessionStorage`.
A cached response paints immediately and starts a network read at the same time.
The fresh response replaces the cached one, and a failed refresh reaches the page's existing error state rather than leaving stale content looking successful.

The cache is scoped to one browser tab session and to a server-issued fingerprint of the viewer's repository and observed-entity access.
The viewer read completes before cached content mounts, so changing accounts cannot paint an answer admitted for the previous viewer even briefly.
Catalog data is not persisted across a browser restart and is not placed in a shared server cache where one viewer's visibility could be served to another.

Hover, keyboard focus, or touch intent on a search result preloads the typed destination.
Selecting a graph node does the same for its entity page.
Successful writes invalidate affected home, graph, note, and entity entries before the UI reloads them.

## Consequences

### Good

- The homepage removes an N-plus-one query whose cost grew with the number of sources.
- Drift's comparison indexes match the questions it actually asks at scale.
- Back navigation and repeat entity reads can paint without waiting for the network.
- Every cached paint is followed by a fresh read, so the acceleration does not become a freshness policy.
- Prefetching uses an action the operator has already signaled rather than downloading every possible entity.
- Session scope avoids a durable store of private catalog content in the browser.
- An access fingerprint prevents two viewers or two permission sets from sharing a cache namespace.

### Bad

- A cached answer can be visible briefly before the fresh answer replaces it.
- The browser performs a network request even on a cache hit, so this reduces perceived latency rather than request count.
- Session storage duplicates data already held in React state and can be unavailable or full; the UI must treat that as a cache miss.
- Mutation invalidation is an explicit list and a new write surface can forget to add its affected reads.
- The application waits for the small viewer read before mounting a route that can consume cached catalog data.
- Four composite indexes add migration and write cost to an index whose source data is rebuilt regularly.

### Rejected because

- A time-based server cache was rejected because invalidation would have to cover reconciles, plugin observations, page changes, viewer visibility, and writes, while a missed invalidation would be served as current.
- Ordinary HTTP freshness caching was rejected because a nonzero freshness lifetime hides changes and a zero lifetime still waits for the response before painting.
- Prefetch without caching was rejected because it helps only the path predicted before a click and does nothing for back navigation or a repeat visit.
