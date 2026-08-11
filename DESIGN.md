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

- **Not a Backstage fork.** You never fork Dusk. You run it and point it at repos. See [ADR-0001](adr/0001-git-as-source-of-truth.md).
- **Not a database-backed wiki.** Content in a DB is content agents cannot read natively and git cannot diff.
- **Not an org chart tool.** Ownership is a field, not a feature.
- **Not a dashboard.** Live state is surfaced next to entities, but Dusk is not replacing Grafana or Glance.
- **Not multi-VCS on day one.** GitHub first, behind an interface that keeps GitHub types out of the core.

## Architecture

Four layers. That is the entire system.

1. **Sources**: repos containing a `dusk.md` at the root, plus whatever ingester plugins emit.
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

### A repo opts in by containing `dusk.md`

A repo participates in the catalog if and only if `dusk.md` exists at its root.
That file is the sole entry point: Dusk reads it and nothing else in the repo, unless `dusk.md` explicitly points at other paths.

No repo is crawled without consent, and consent is expressed by a file rather than by central registration.
Cold start is solved by installation, since granting access to repos that already contain `dusk.md` populates the catalog immediately.

Write routing falls out of this for free.
An entity's home repo is wherever its `dusk.md` lives.

Full reasoning: [ADR-0004](adr/0004-dusk-md-convention.md).

### Everything catalog-shaped is markdown

The config repo contains markdown and nothing else.
A source, a plugin's configuration, layout, theme: each is a markdown file whose frontmatter carries the settings and whose prose explains what the thing is and why it exists.

This is a deliberate constraint rather than an aesthetic one.
Config that is forced to document itself stays understandable, and agents handle markdown better than they handle bare YAML.

Frontmatter is schema-validated, with errors that name the file, the field, and the expectation.

**The carve-out**: boot configuration is not catalog content.
Listen address, storage path, credentials, and which repos to track are how the process starts, not something the catalog describes.
That stays in `dusk.yaml` or the environment.

### PR previews are a primitive, not a feature

Because the index is ref-keyed, any open PR can be rendered as the catalog *as it would be after merge*.

Two things fall out for free:

- **Semantic diffs.** "PR 112 adds 1 service, removes 2 gotchas, reassigns 3 owners, breaks 1 dependency link." GitHub shows changed YAML. Dusk shows what it means.
- **A PR comment bot** posting the preview link and that summary.

This is also the trust story.
An agent proposes, a PR opens, the bot summarizes, a human reviews the *rendered result* rather than a diff, and merges.

### One write path, three access modes

MCP writes, UI edits, and a human editing a file by hand all take the same route: file edit, commit, reconcile.

Access mode is chosen at install time and is changeable later:

- **Read**: Dusk never writes to source repos. Proposed changes surface in the UI and go no further.
- **Proposal**: Dusk opens pull requests against the repo that owns the entity, so that repo's own owners review changes to their own catalog entry.
- **Write**: Dusk commits directly.

Because writes are file edits, existing review gates, `git diff`, and `git log` all work unchanged.
No new trust model is required, which is the point.

### Credentials are a GitHub App, registered for the user

Dusk registers its own GitHub App via the App Manifest flow.
It POSTs a manifest, GitHub redirects back with a temporary code, and Dusk exchanges that code for the app id, private key, and a GitHub-generated webhook secret.

The user's only manual step is accepting and installing it.
There is no personal access token path, because the manifest flow removes the onboarding friction that would justify one.

All GitHub interaction goes through a single internal source interface, and no GitHub type crosses that boundary into the reconciler.

Full reasoning: [ADR-0005](adr/0005-github-app-and-access-modes.md).

### Reconcile is webhook-triggered with a poll floor

Webhook deliveries trigger immediate reconcile.
A periodic poll runs regardless, on a slow interval, comparing refs with `git ls-remote`.

Poll-only is a fully supported configuration for anyone without a public endpoint, not a degraded one.

The poll floor is load-bearing.
Webhook deliveries are lost in normal operation, and a system with no poll underneath goes silently stale with no signal, which for this product is the worst available bug.

Full reasoning: [ADR-0006](adr/0006-reconcile-triggering.md).

### Plugins are subprocesses over one schema

The `.proto` is the single source of truth for entity types, versioning, and validation.
One contract, two transports:

- **Tier 1, ingesters.** Exec a binary, it writes protojson on stdout, Dusk ingests. Any language, testable with `./my-plugin | jq`, and a plugin can be a shell script. Ships first.
- **Tier 2, interactive plugins.** The identical messages over gRPC on a host-provided unix socket. Host owns lifecycle.

Ingesters are how the catalog covers infrastructure that has no repo of its own.
They are not discovery: an ingester is explicitly configured, and then declares in bulk whatever it finds.

Explicitly **not** `hashicorp/go-plugin`, despite it being the proven pattern.
Its handshake is Go-host-centric, and writing a non-Go plugin against it is painful enough to quietly falsify the language-agnostic promise.

Full reasoning: [ADR-0002](adr/0002-plugin-protocol.md).

### The entity schema is small, with open kinds

Four message types: **Entity** (with an open `kind` field), **Relation** (typed edges), **Note** (the "how do I do this again" unit), and **Observation** (ingester-emitted status).

Four rules keep the `.proto` from becoming a trap.
Kinds and relation types are open strings rather than closed enums, so the taxonomy grows without a release.
Every entity has a stable `ref` of the form `kind:namespace/name`, which is the correlation key.
Every message carries `source` and `observed_at`.
An `attributes` map is the escape hatch, and recurring attributes get promoted to real fields.

**Declaration and observation are different layers, not competing claims.**
`dusk.md` declares intent, ingesters observe reality, and an entity carries both facets.
Divergence is surfaced as **drift**, which is a feature: "your `dusk.md` says mini-1, the ingester found mini-2."

Published as `v1alpha`, with no stability promised until the three in-house ingesters have shipped.

Full reasoning: [ADR-0007](adr/0007-entity-schema.md).

### The graph is stored in SQLite

One database file, `ref` as a column, WAL mode.
Garbage collection on PR close is `DELETE WHERE ref = ?`.
Relation traversal uses recursive CTEs and search uses FTS5, both as raw SQL.

Accessed through GORM on the **`github.com/glebarez/sqlite`** driver.
That driver choice is load-bearing: the default `gorm.io/driver/sqlite` requires cgo, which would break cross-compilation, distroless images, and arm64 builds.

FTS5 is the strongest argument here, because search is a core product requirement and this puts it in the storage layer rather than in a service added later.

Full reasoning: [ADR-0008](adr/0008-storage.md).

### GitOps, never a fork

Backstage's fatal flaw is that it is a source distribution.
You clone `create-app`, own a Node monorepo forever, and every upgrade is a merge conflict.

Dusk ships as a binary and a container.
You install the App, point it at repos, and it reconciles.

### Sync observability from day one

The worst part of operating Flux is not knowing whether something synced, and why not.

Last reconcile, what changed, what failed, what is stale: shipped in the first release, not added later as polish.
It doubles as the agent-trust surface, because a feed of what the agents changed is what makes someone comfortable enabling writes.

### Licensing

Apache 2.0, with the entity schema and `.proto` explicitly covered so plugin authors have no license anxiety.
DCO rather than a CLA.

Full reasoning: [ADR-0003](adr/0003-license.md).

## Look and feel

**Dark only.** There is no light mode.

The name is the theme: dusk is the hour owls hunt, and Dusk is a tool for finding things you cannot clearly see.
The palette is **Dracula Pro**.

CSS is authored with custom properties so that custom themes are possible, but Dusk ships exactly one palette and maintains exactly one palette.

## Open questions

The architectural decisions are settled and recorded in [adr/](adr/).
What remains is undecided but does not block starting.

### What does the MCP server actually expose?

Which tools, at what granularity.
Too fine and an agent burns a dozen calls answering one question; too coarse and it cannot ask anything specific.
This shapes how good Dusk feels to the primary consumer, so it deserves real thought rather than a mechanical mapping of the schema.

### How does an agent curate the layout?

Layout is markdown in the config repo, so an agent can already edit it.
What is undecided is how much structure that markdown carries, and how a layout change is reviewed before it lands.

### How are ingesters scheduled?

Ingesters are configured as markdown, but their cadence, concurrency, failure handling, and API budget are not specified.
Rich ingesters are where REST quota actually burns, per [ADR-0006](adr/0006-reconcile-triggering.md), so this needs a real answer before the second ingester ships.

### Who can view Dusk?

The GitHub App governs what Dusk can read and write, not who can read Dusk.
A private catalog rendered on a public URL needs its own authentication story, and it is not yet designed.
