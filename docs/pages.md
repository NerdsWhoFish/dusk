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
| `entities` | Entities matching a query |
| `recent-notes` | Notes, newest first, narrowed by `query` |
| `drift` | Where the catalog and reality disagree |
| `integrity` | What is wrong with the catalog itself |
| `kinds` | How many entities of each kind, which is the estate's shape |
| `reads` | What Dusk last read, per repository |
| `view` | A plugin's own view, declared or drawn ([ADR-0020](../adr/0020-plugin-ui.md)) |

Every block takes `title` and `wide`. `wide` is a hint asking for the full width, not a layout: blocks stay queries.

Every block resolves for whoever is looking.
A page declares one set of blocks and a restricted viewer gets their own answer to each: `entities` lists only what they can read, and `kinds`, `drift` and `integrity` count and compare only over that same half ([ADR-0051](../adr/0051-a-count-is-of-what-the-viewer-can-see.md)).
Two people can therefore see different numbers on one declared page, which is the point rather than a bug.
`reads` is the exception and still names every repository.

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

## Querying notes

```yaml
  - type: recent-notes
    title: Ideas
    query: kind:idea status:open
    limit: 8
```

| Part | Meaning |
| --- | --- |
| `kind:idea` | Only that kind of note |
| `status:open` | Only what is still open. Also matches a note written before there were statuses, because empty means open |
| `ref:service:home/jellyfin` | Only notes about that entity |
| `limit:` | How many to keep |

An **idea** is a note of kind `idea`, and an idea block is what makes it worth capturing one: somewhere to see what you thought of and have not done.
A note that is work carries a control to mark it done or drop it, and closing one from a block is a write proved by the read that put it on the page.

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

A plugin contributes views two ways ([ADR-0020](../adr/0020-plugin-ui.md)): a **declared** one, which is a description Dusk renders itself with no JavaScript from the plugin, and a **drawn** one, which is a custom element the plugin ships.
A `view` block mounts either.
On an entity page Dusk mounts them automatically; on a portal page there is no entity, so the block says what to render over:

```yaml
  - type: view
    title: Flights through ATL
    plugin: airtrail
    ref: airport:airtrail/atl
    wide: true
```

A plugin may contribute several views, so `element` names which one.
It matches a drawn view by its element tag, and a declared view by its title, because a declared view has no tag and its title is the only name it has.
With a single view it is inferred; with more than one and no `element`, the block says so and lists them by whichever name selects them, rather than picking one, because rendering a view the page did not ask for looks like the block being wrong instead of incomplete.

What a contribution declares it applies to is ignored here.
Those kinds say which entity pages it mounts on, and this block supplies its own `ref` or `query` instead.

A view about the plugin rather than about one entity mounts on the plugin's own page and is never offered to a portal page.
That slot supplies no result set, so only a drawn view works there ([ADR-0064](../adr/0064-a-declared-view-mounts-where-a-result-set-comes-from.md)).

Nothing renders if that plugin is not installed and running, because only a live plugin can say what it contributes.

### Rendering your own query through a plugin's view

A view may take a `query` instead of a `ref`. Dusk resolves it exactly as an `entities` block would and hands the results to the view, so the page asks the question and the plugin decides only how the answer looks:

```yaml
  - type: view
    title: Recent flights
    plugin: airtrail
    query: kind:flight
    sort: -date
    limit: 5
```

A declared view draws those results directly, one row or chip per entity, following the fields the plugin named.

A drawn one is handed them as a **property** rather than an attribute, because stringifying a result set into markup would be absurd. An element that wants them implements a setter:

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
