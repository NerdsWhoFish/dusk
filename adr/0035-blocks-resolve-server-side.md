# 35. A page's blocks resolve on the server

Date: 2026-08-12

## Status

Accepted. Implements [ADR-0013](0013-layout-and-pages.md).

## Context and Problem Statement

[ADR-0013](0013-layout-and-pages.md) settled that a page is a markdown file whose frontmatter declares an ordered list of typed queries, and that a block is a query rather than a placed widget.
It did not settle where the query runs.

The first UI shipped with the blocks hardcoded in React, which is the shape [ADR-0013](0013-layout-and-pages.md) exists to avoid: the layout was a component tree, so changing it meant a release rather than an edit.

Two things make the answer non-obvious.

**The same page has two render targets.** [ADR-0014](0014-agent-context-injection.md) requires the page representation to render into an agent's context as well as a browser. A resolution that lives in the browser cannot serve the agent.

**Blocks are queries against the index**, which is SQLite inside the server. Anything resolving them elsewhere is either reimplementing the queries or making one HTTP request per block.

## Considered Options

1. **Client side.** Ship the declared blocks to the browser; it calls an endpoint per block type.
2. **Server side.** Resolve every block against the index and send the results.
3. **Hybrid.** Resolve cheap blocks on the server, let expensive ones stream in.

## Decision Outcome

Chosen: **option 2**.

`GET /api/home` returns the page's title, its prose, and every block with its query already run.

### One request, one implementation

A page with six blocks is one round trip rather than seven, which matters most on the first screen anybody loads.

More importantly there is one implementation of "what does an `entities` block with this query mean".
Under option 1 the browser would own that meaning and [ADR-0014](0014-agent-context-injection.md)'s agent renderer would need its own copy, which is exactly the divergence that made three glob implementations disagree in [ADR-0032](0032-tarball-reads.md)'s amendment.

### A failing block renders empty and says why

[ADR-0013](0013-layout-and-pages.md) requires that a bad query degrade safely.
A block that fails carries its error rather than propagating it, so one broken block cannot take the page down, and the reason travels with the block rather than into a log nobody reads.

The same rule covers a page that does not parse at all: the default page is served, with the parse error attached, rather than an error page where the catalog should be.

### The query language stays small on purpose

A block's query is bare words plus `kind:`, and nothing else.

A query language is a thing to design rather than to grow, and every operator it gains has to be implemented once here and understood everywhere a page is authored.
Bare words go through search, which is ranked; a bare `kind:` goes through list, which is ordered and complete.
When something genuinely needs more, that is its own decision.

### Declaring stays optional

The default page is defined in Go and covers kinds, drift, notes, integrity and read status.
[ADR-0013](0013-layout-and-pages.md) is explicit that a catalog which only looks presentable once everyone does homework is the failure Dusk exists to avoid, so the declared page is for the minority that wants something specific.

## Consequences

### Good

- The layout is data, editable in the config repository, and changing it is a commit rather than a release.
- One implementation of every block type, shared by the browser and by whatever renders a page into an agent's context.
- One request for a whole page.
- A broken block, a broken query, and a page that does not parse all degrade to something readable.

### Bad

- **A slow block makes the whole page slow**, because the response waits for all of them. Option 3 exists for exactly this and is the thing to reach for when a block starts costing real time.
- Resolution happens per request with no caching, so a page with an expensive block pays for it on every load.
- The query language will be asked to grow, and each addition is a decision that has to be made once and lived with.
- Pages are read through the write path, which means a page render depends on GitHub being reachable. A cached copy would decouple them, and does not exist.

### Rejected because

- **Option 1** was rejected because it puts the meaning of a block in the browser, where the agent renderer cannot reach it, and turns one page into a request per block.
- **Option 3** was rejected as premature. It is the right answer once a block is measurably slow, and adopting it early would add streaming to a page that renders in one query today.
