# 13. Pages are markdown, blocks are queries, and satellite repos own their entity page

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk has a web UI, and one of the stated goals is that an agent can curate it.
That requires a layout representation an agent can safely edit.

Hand-placed widgets with coordinates are a poor fit: an agent editing positions produces broken layouts, and there is no sensible way to review the change.

Separately, repos that contain a `dusk.md` per [ADR-0004](0004-dusk-md-convention.md) should be able to say how their own entities are presented.
Allowing that naively creates route collisions and a content injection surface, because a repo granted read access would be placing content in the portal's origin.

## Considered Options

For layout representation:

1. **Positioned widgets** with explicit geometry.
2. **Blocks as queries**, rendered in order.

For who may declare pages:

1. Config repo only.
2. Any repo, any route.
3. **Two page classes with different owners.**

## Decision Outcome

### Blocks are queries

A page is a markdown file whose frontmatter declares an ordered list of blocks.
A block is a typed query, not a placed widget.

```yaml
---
title: Home
blocks:
  - type: entities
    query: kind:service runs_on:prod-2
  - type: drift
  - type: recent-notes
    limit: 10
---
```

An agent curating the layout is therefore an agent writing and tuning queries, which is something agents do well.
It also degrades safely: a bad query renders empty rather than breaking the page.

### Two page classes

- **Entity pages** are owned by the satellite repo and rendered at the entity's canonical route. Every entity gets a good default page with no configuration, and a repo's declaration is an **override**.
- **Portal pages** are owned by the config repo only: home, cross-cutting views, navigation.

Route collisions are impossible by construction, because entity routes derive from entity refs and satellites cannot claim arbitrary paths.

### Three constraints on satellite pages

- Queries default to that repo's own entities, with explicit opt-in to query wider, so a satellite page cannot accidentally surface other people's entities.
- Navigation stays config-repo-owned. A satellite declares its page but cannot force itself into the nav.
- No raw HTML and no external embeds. Prose is sanitized markdown. The blocks-as-queries decision already limits free-form content to prose, so this constraint is cheap.

### Defaults must be good enough that declaring is optional

If a repo must write a page to look presentable, the catalog only works when everyone does homework, which is the failure mode Dusk exists to avoid.
Declaration is for the minority that wants something specific.

### Pages have two render targets

The same page representation renders to a browser or to an agent's context, per [ADR-0014](0014-agent-context-injection.md).
One layout system, two renderers.

## Consequences

### Good

- Queries are reviewable in a way geometry is not. A diff of a query says what changed about the meaning of the page.
- Agents can curate layout without being able to break it, because the worst outcome of a bad query is an empty block.
- Two page classes eliminate route collisions structurally rather than by policy.
- Sanitization is cheap because the design already limits free-form content, so the injection surface stays small without extra work.
- Two render targets mean the agent-facing context is curated by the same mechanism, with the same review path, as the human-facing UI.

### Bad

- Blocks-as-queries constrains visual design. Anything the block vocabulary does not express cannot be built, and expanding that vocabulary is a code change rather than a config change.
- Good defaults are real work, and they are the thing most likely to be under-invested in while attention goes to the configurable path.
- Two ownership classes means two permission paths through the page renderer.
- Satellite pages that opt into wider queries are a legitimate feature and also the mechanism by which someone could surface things others did not expect them to.

### Rejected because

- Positioned widgets were rejected because agents edit them badly and diffs of geometry are unreviewable.
- Config-repo-only pages were rejected because a service's own team knows best how to present it, and centralising that recreates the bottleneck the `dusk.md` convention removed.
- Any repo claiming any route was rejected as a collision and hijacking surface.
