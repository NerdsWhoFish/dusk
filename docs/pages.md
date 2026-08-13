# The portal page

The homepage is a file in the config repository: `.dusk/home.md`.

Without it Dusk serves a default page, which has to be good enough that declaring one is optional ([ADR-0013](../adr/0013-layout-and-pages.md)).
With it, **what you declare is the whole page**. Blocks are not merged with the defaults, so declaring one block gives you a page with one block, and that is how everything currently on the homepage is removed.

## Shape

```markdown
---
title: Home
blocks:
  - type: kinds
  - type: drift
    title: Drifted
    limit: 6
---

Anything below the frontmatter is prose, rendered above the blocks.
```

A block is a **query, not a widget** ([ADR-0013](../adr/0013-layout-and-pages.md)).
You say what to ask; the renderer decides how a result looks.
That is what makes the layout something an agent can curate: tuning a query is work agents are good at, and a bad query renders empty rather than breaking the page.

## The default page

This is exactly what Dusk serves when the file is absent, so it is also the starting point for editing it.
Paste it, then change what you want:

```yaml
---
title: Home
blocks:
  - type: kinds
  - type: drift
    title: Drifted
    limit: 6
  - type: reads
    title: What Dusk has read
  - type: recent-notes
    title: Recent notes
    limit: 5
    wide: true
  - type: integrity
    title: Problems
    wide: true
---
```

The order is not accidental: it pairs a tall block with a short one, because two columns with a single short block in a row reads as a missing panel rather than a layout.

## Block types

| Type | What it asks |
| --- | --- |
| `entities` | Entities matching a query. The only type that takes one |
| `recent-notes` | The most recently written notes |
| `drift` | Where the catalog and reality disagree |
| `integrity` | What is wrong with the catalog itself |
| `kinds` | How many entities of each kind, which is the estate's shape |
| `reads` | What Dusk last read, per repository |
| `view` | A plugin's own custom element ([ADR-0020](../adr/0020-plugin-ui.md)) |

Every block takes `title` and `wide`. `wide` is a hint asking for the full width, not a layout: blocks stay queries.

## Querying entities

```yaml
  - type: entities
    title: Latest flights
    query: kind:flight
    sort: -date
    limit: 3
```

| Part | Meaning |
| --- | --- |
| `kind:flight` | Only that kind |
| `related:airport:airtrail/atl` | Only entities with a relation to that ref, **in either direction** |
| bare words | Full-text over name and title |
| `sort:` | `name`, or any attribute key. A leading `-` reverses it |
| `limit:` | How many to keep, applied after sorting |

Direction is ignored for `related:` on purpose.
Asking what you have flown through an airport should not mean writing one block for departures and another for arrivals.

Sorting by an attribute is what makes "the latest three" possible, and it is why a plugin that wants its entities ordered should publish the thing they are ordered by as an attribute rather than burying it in the description.

Two blocks answering the questions this was built for:

```yaml
  # The three most recent flights.
  - type: entities
    title: Latest flights
    query: kind:flight
    sort: -date
    limit: 3

  # Everything that has touched one airport, whichever way it went.
  - type: entities
    title: Through ATL
    query: related:airport:airtrail/atl
    wide: true
```

## Showing a plugin's own view

A plugin may ship a custom element ([ADR-0020](../adr/0020-plugin-ui.md)). On an entity page Dusk mounts it automatically. On the homepage there is no entity to mount it against, so the block says which:

```yaml
  - type: view
    title: Flights through ATL
    plugin: airtrail
    ref: airport:airtrail/atl
    wide: true
```

A plugin may contribute several views, so `element` names which one.
With a single view it is inferred; with more than one and no `element`, the block says so and lists them rather than picking one, because rendering a view the page did not ask for looks like the block being wrong instead of incomplete.

Nothing renders if that plugin is not installed and running, because only a live plugin can say what it contributes.

### Rendering your own query through a plugin's component

A view may take a `query` instead of a `ref`. Dusk resolves it exactly as an `entities` block would and hands the results to the element, so the page asks the question and the plugin decides only how the answer looks:

```yaml
  - type: view
    title: Recent flights
    plugin: airtrail
    query: kind:flight
    sort: -date
    limit: 5
```

The results arrive as a **property** rather than an attribute, because stringifying a result set into markup would be absurd. An element that wants them implements a setter:

```js
set entities(value) {
  this._entities = value ?? null;
  this.render();
}
```

An element with no such setter simply ignores them and falls back to whatever it does with `entity-ref`, so adding a query to a view never breaks it.

## Removing the search box

The search box is not a block, and it is shown unless the page says otherwise:

```yaml
---
title: Home
search: false
blocks:
  - type: kinds
---
```

Absent means yes. Search is the primary action, so a page that forgets to mention it still gets one.

## Editing it over MCP

The `page` tool is both halves. Called with no `body` it returns the current page, or the default written out when none is declared, along with a proof token. Called with a `body` it replaces the page, and requires that token.

Reading first is the point: what you send becomes the **whole** page, blocks are not merged, and a layout is worth looking at before replacing. That is the same read-before-write contract every other write obeys ([ADR-0009](../adr/0009-proof-tokens.md)).

A page that would not parse is refused with the reason. It is never committed and discovered later as a blank homepage.
