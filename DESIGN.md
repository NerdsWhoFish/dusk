# Dusk: Design

> Status: draft, pre-implementation.
> This document records decisions already argued out.
> Anything still contested lives under [Open Questions](#open-questions).

## What Dusk is

Dusk is a service catalog and knowledge layer that **maintains itself**, because the agents doing the work also do the documenting.

It answers two questions that get harder as a system grows:

- *Where is this thing, and what does it depend on?*
- *How do I do this thing again?*

Humans browse it.
Agents read and write it over MCP.
Git is the source of truth for both.

## The thesis

Every knowledge tool dies the same death: the curation burden falls on a human who has better things to do.
Backstage, Confluence, Notion, wikis, hand-maintained README tables: all of them rot for the same structural reason.
The person who knows the thing is the person doing the work, and they are busy doing the work.

That constraint just changed.
The thing doing the work is now also a thing that can write.
If the catalog updates as a *byproduct* of an agent completing a task, drift stops being the default state.

That is the whole product.
Everything below is in service of it.

## Non-goals

- **Not a Backstage fork.** You never fork Dusk. You run it and point it at a repo. See [ADR-0001](adr/0001-git-as-source-of-truth.md).
- **Not a database-backed wiki.** Content in a DB is content agents cannot read natively and git cannot diff.
- **Not an org chart tool.** Ownership is a field, not a feature.
- **Not a dashboard.** Live state is surfaced next to entities, but Dusk is not replacing Grafana or Glance.

## Architecture

Four layers. That is the entire system.

1. **Sources**: git repos containing markdown with frontmatter, plus whatever ingester plugins emit.
2. **Reconciler**: reads sources at a given git ref, builds the typed entity graph.
3. **Serving**: the MCP server (agents) and the web UI (humans), both reading the same materialized graph.
4. **Write path**: MCP and UI writes become file edits, which become commits, which reconcile back in.

The graph is always *derived*.
Delete the index and rebuild it from git at any time, with no data loss.

## Core decisions

### Git is the source of truth, and the index is keyed by ref

Entities live as markdown with frontmatter in the repos they describe, not in a parallel YAML file and not in a database.
Metadata sits in the doc it documents.

The reconciler is `reconcile(ref)` from the first commit, never `reconcile()`.
Multiple materialized views are alive at once, and ephemeral ones are garbage-collected when their PR closes.

This is what makes `dusk.example.com?pr=112` nearly free.
Deferring it means rewriting the storage layer later.

Full reasoning: [ADR-0001](adr/0001-git-as-source-of-truth.md).

### PR previews are a primitive, not a feature

Because the index is ref-keyed, any open PR can be rendered as the catalog *as it would be after merge*.

Two things fall out for free:

- **Semantic diffs.** "PR 112 adds 1 service, removes 2 gotchas, reassigns 3 owners, breaks 1 dependency link." GitHub shows changed YAML. Dusk shows what it means.
- **A PR comment bot** posting the preview link and that summary.

This is also the trust story.
An agent proposes, a PR opens, the bot summarizes, a human reviews the *rendered result* rather than a diff, and merges.

### One write path

MCP writes, UI edits, and a human editing a file by hand all take the same route: file edit, commit, reconcile.

Two commit modes, one engine:

- **Direct**: commit straight to the branch. Correct for a single operator.
- **Proposal**: open a PR for review. Correct for a team that would never let an agent write to main.

Because writes are file edits, existing review gates, `git diff`, and `git log` all work unchanged.
No new trust model is required, which is the point.

### Plugins are processes, not a linked API

Two tiers:

- **Tier 1, ingesters.** Exec a binary, it writes entities as JSON on stdout, Dusk ingests. Schema-versioned. Any language, testable with `./my-plugin | jq`, and a plugin can be a shell script.
- **Tier 2, interactive plugins.** Protobuf service over a gRPC connection on a unix socket the host provides. Host owns lifecycle.

Tier 1 ships first, and most plugins will never need Tier 2.

Explicitly **not** `hashicorp/go-plugin`, despite it being the proven pattern.
Its handshake is Go-host-centric, and writing a non-Go plugin against it is painful enough to quietly falsify the language-agnostic promise.

Full reasoning: [ADR-0002](adr/0002-plugin-protocol.md).

### GitOps, never a fork

Backstage's fatal flaw is that it is a source distribution.
You clone `create-app`, own a Node monorepo forever, and every upgrade is a merge conflict.

Dusk ships as a binary and a container.
You point it at a config repo and it reconciles.

The config repo stays **small**: which sources to watch, which plugins to run, layout, theme.
Entities are *discovered* from frontmatter in the repos they describe, and config only overrides.

The failure mode to avoid is Backstage's, where "point it at a repo" degrades into four hundred lines of declarative YAML per service.

### Sync observability from day one

The worst part of operating Flux is not knowing whether something synced, and why not.

Last reconcile, what changed, what failed, what is stale: shipped in the first release, not added later as polish.
It doubles as the agent-trust surface, because a feed of what the agents changed is what makes someone comfortable enabling writes.

### Licensing

Apache 2.0, with the entity schema and `.proto` explicitly covered so plugin authors have no license anxiety.
DCO rather than a CLA.

Full reasoning: [ADR-0003](adr/0003-license.md).

## Look and feel

Dark by default, using the **Dracula Pro** palette.
The name is the theme: dusk is the hour owls hunt, and Dusk is a tool for finding things you cannot clearly see.

Light mode exists but is not the identity.

## Open questions

These are genuinely undecided and should be resolved before the code that depends on them is written.

### Does the reconciler pull, or do sources push?

Pull (polling watched repos) is simpler and matches Flux.
Push (webhooks) is faster and is a pattern already proven in `perch`.

Leaning pull first, with push as an optimization.

### How much of the catalog is discovered versus declared?

Full discovery is magic when it works and infuriating when it guesses wrong.
Leaning discover-by-default with an explicit override file, but this knob shapes how the entire product feels.

### Where do agent writes land?

When an agent learns a fact about a service whose docs live in a different repo than the config, which repo receives the commit?

An entity must declare its home and writes must follow it.
Getting this wrong means everything piles into one repo and the duplication problem is recreated inside Dusk.
