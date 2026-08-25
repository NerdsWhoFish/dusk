# 78. The estate map is a progressive graph with a touch-first fallback

Date: 2026-08-25

## Status

Accepted

## Context and Problem Statement

Dusk stores an entity graph but the browser renders relations only as lists on individual entity pages.
That makes the estate searchable but not explorable: an operator cannot see clusters, bridges, or isolated entities without already knowing which ref to open.

A real catalog can hold thousands of entities and several thousand edges.
Putting that whole payload and a force layout in the homepage response would delay the primary search flow and would turn a phone into a poor imitation of a desktop graph.

The graph renderer also creates a dependency question.
Dusk could draw its own SVG or canvas, use a React node-editor library, or use a graph library whose model is already nodes and edges.

## Considered Options

1. Draw and lay out the graph in Dusk with SVG or canvas.
2. Use a React flow editor.
3. Use Cytoscape.js behind a progressive estate-map block.
4. Keep relation lists and add no graph.

## Decision Outcome

Use Cytoscape.js for the fine-pointer graph and load it through a dynamic import only after the homepage has painted.
Cytoscape.js is MIT licensed, renderer-agnostic, mature, and owns force layout, pan, zoom, selection, and dragging without turning Dusk into a graph-layout project.

The default homepage includes a wide `graph` block.
The block loads a lean `/api/graph` payload separately from `/api/home`, because the graph is the largest read on the page and is not required before search works.
The endpoint returns one resolved node per entity ref, only relations whose two ends are visible, and the notes attached to each node.

The first view shows the 160 most connected nodes.
Expanding adds another wave, while search always reaches every node in the payload and selecting one brings its immediate neighbors into view.
This keeps the initial layout bounded without pretending the visible wave is the whole estate; the block always states how many nodes it is showing.

Selecting a node exposes its attached knowledge and preloads the full entity read.
A node with knowledge is marked distinctly in the graph.

On a coarse pointer the same query renders as a searchable list and node briefing rather than instantiating the force graph.
The entity and its knowledge remain reachable, satisfying [ADR-0025](0025-responsive-ui.md) without making touch interaction depend on precise dragging.

## Consequences

### Good

- The relationship structure becomes explorable from the default page rather than remaining an implementation detail of the index.
- Search, progressive waves, and neighbor expansion keep a large estate usable without silently truncating it.
- Notes travel with their node, so the map shows operational knowledge rather than only topology.
- Dynamic loading keeps Cytoscape.js out of the initial JavaScript chunk.
- The touch fallback preserves the task instead of shrinking a desktop interaction until it is frustrating.

### Bad

- Cytoscape.js is a large dependency and a second JavaScript chunk the browser eventually downloads on a fine pointer.
- A force layout is not stable between graph changes, so this is an explorer rather than a diagram with durable coordinates.
- The first wave emphasizes highly connected nodes and can under-represent isolated but important entities until they are searched or the map is expanded.
- The graph payload is another HTTP request and can be large even though it omits descriptions and attributes.

### Rejected because

- A custom SVG or canvas renderer was rejected because layout, zoom, hit testing, selection, accessibility, and drag behavior are a mature library's job rather than Dusk's product idea.
- A React flow editor was rejected because the estate is a graph to inspect rather than a workflow to author, and an editor-oriented node model adds controls and semantics Dusk does not need.
- Keeping only lists was rejected because it preserves exact local detail while leaving the estate-level relationship question unanswered.
