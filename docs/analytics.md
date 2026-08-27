# Local dashboard analytics

The `analytics` page block turns Dusk's current local state into an operator dashboard.
It does not install a tracker and does not send analytics anywhere.

## What it measures

- **Current catalog** is the number of distinct entities in the default catalog view.
- **Declared sources** ranks repositories by the distinct entities each one contributes.
- **Knowledge** counts notes and open work, shows the most common note kinds, and ranks notes by how many catalog refs they connect.
- **Plugin activity** ranks plugins by invocations in Dusk's retained action history, with completed outcomes and the last recorded use.

These labels are deliberate.
A large repository is not necessarily the one a person visits most, and a well-connected note is not necessarily the note edited most often.
Dusk does not claim either.

## What it does not collect

Dusk records no page views, search text, IP addresses, browser identities, or raw action parameters for this feature.
The snapshot is derived when the home API is read.
It uses the materialized catalog and the bounded local action log, performs no GitHub calls, and remains useful when GitHub is unavailable ([ADR-0088](../adr/0088-dashboard-analytics-are-derived-locally.md)).

The action count is a retained window rather than a lifetime counter.
Old actions leave that window as new ones arrive.

## Put it on a page

```yaml
- type: analytics
  title: Estate pulse
  wide: true
```

The block accepts `title` and `wide` like every other page block.
It has no query or limit because each section has a fixed, bounded ranking and the block always describes the complete operator catalog.
