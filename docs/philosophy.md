# Philosophy

How Dusk is built, and why.
Decisions live in [`adr/`](../adr/); this is the standing posture those decisions are made from.

## 1. Agents are a first-class consumer, not an integration

Dusk has two interfaces over one graph.
The MCP server is how agents read and write; the web UI is how humans read and write.
Neither wraps the other, and neither is a courtesy layer bolted on afterwards.

The catalog exists because agents can maintain it as a byproduct of doing work.
That is the thesis, so the agent-facing surface gets the same design attention as the human-facing one, and the write path is identical for both.

## 2. Decisions are recorded as ADRs

Technical decisions are written as [MADR](https://adr.github.io/madr/) records in `adr/`: context, considered options, decision, and consequences both good and bad.

The value is not the decision, it is the **rejected alternatives** and the reasoning.
That is what stops the same argument happening again in six months.

A reversed decision gets a new ADR that supersedes the old one. A decision is never rewritten.

Wording that has gone stale may be amended in place, recorded in a dated section at the bottom of the file, but the decision and the reasoning behind it are never touched ([ADR-0028](../adr/0028-amending-adrs.md)).

## 3. Anomalies are surfaced, never silenced

This is the strongest rule in the document, because Dusk's failure modes are quiet.

A stale entity that looks current, a duplicate note nobody prunes, an ingester timeout mistaken for a decommissioned service: none of those crash, and all of them destroy the only thing the product sells.

So Dusk never papers over ambiguity:

- A failing ingester **never deletes**. It marks observations stale and says so ([ADR-0011](../adr/0011-ingester-scheduling.md)).
- A declaration disagreeing with an observation is **drift**, surfaced as a finding rather than merged away ([ADR-0007](../adr/0007-entity-schema.md)).
- A near-duplicate note is created **with a warning** naming what it resembles ([ADR-0010](../adr/0010-mcp-surface.md)).
- A rejected write returns **the exact call that fixes it**, not a status code ([ADR-0009](../adr/0009-proof-tokens.md)).
- Reconcile status is a first-class surface from the first release, not later polish.

No silent defaults, no swallowed errors, no stack traces where an actionable message belongs.

## 4. Engineering rigor, no exceptions

Every change goes through review, tests, and green CI.
No cowboy fixes.

Coverage percentage is the wrong target. Dusk tests **invariants**, not tautologies, and every load-bearing ADR rule has a test named after it.

Full policy: [ADR-0017](../adr/0017-engineering-policy.md).

## 5. Minimal dependencies

Standard library when it gets the job done.
Every dependency is a thing to audit, update, and eventually regret.

**No cgo without an ADR.** It costs cross-compilation, distroless images, clean `-race`, and reproducible builds, and it usually arrives transitively without anyone choosing it.

Dusk does ship as a container alongside the binary, and that is deliberate.
It runs as a service with a UI and a reconcile loop, so a container is a distribution format rather than a runtime dependency.
Nothing about Dusk should require Docker to develop or test.

## 6. Solve actual problems

Every feature is justified against a real need.

This deserves saying plainly on a project designed this fast: AI makes it cheap to produce features and cheap to produce plausible-sounding rationales for them.
Neither is a reason to build something.

The question is whether it solves a problem someone actually has.
If it does not, it does not belong, however elegant it is.

## 7. Structured logging, for two audiences

Every operation emits structured logs with severity, entity context, and a trace id.

The two audiences differ.
Agents parse errors programmatically to diagnose and recover, so errors are machine-readable and say what to do next.
Humans get terse, actionable output, while the full structured log is always written.

A trace id follows one entity across the whole pipeline, from ingest through reconcile to render.
When something looks wrong on a page, that is how you find which ingest produced it.

## 8. Reconcile is expensive, everything after it is local

Hitting GitHub and cluster APIs is the costly operation: rate-limited, slow, and failure-prone.
Everything downstream reads the local index.

That is why the index is a real queryable store rather than a cache ([ADR-0008](../adr/0008-storage.md)).
Search, traversal, rendering, and PR previews are fast because none of them touch the network.

## 9. Opinionated, but every opinion is a seam

Dusk ships strong defaults: a default page for every entity, a block vocabulary, well-known entity and note kinds, three access modes.
Out of the box it should do the right thing without configuration.

Every opinionated choice is a **seam, not a wall**, and the kind of seam varies.
A different data source is a plugin ([ADR-0002](../adr/0002-plugin-protocol.md)).
A different presentation is a page declaration in markdown ([ADR-0013](../adr/0013-layout-and-pages.md)).
A different vocabulary is `mintKind`.

Most users should never need to customise. The ones who do should not need to fork.

## 10. Security-conscious by default

Dusk holds sensitive material: a GitHub App private key, installation tokens, a webhook secret, and eventually credentials capable of mutating infrastructure ([ADR-0015](../adr/0015-plugin-actions-and-events.md)).

Secrets are never logged, never in CLI or UI output, and never committed.
Structured logs redact them.

Risky capability is opt-in and explicit.
Actions are denied by default, and installing a plugin never silently grants one.

## 11. Dusk is not the system of record

Dusk describes systems it does not own.
The cluster owns its workloads, the repo owns its code, GitHub owns access.

Writes are the exception rather than the rule, and they carry obligations.
Dusk writes only catalog data, only to repos that opted in by containing a `dusk.md` ([ADR-0004](../adr/0004-dusk-md-convention.md)), and only in the mode the user chose ([ADR-0005](../adr/0005-github-app-and-access-modes.md)).
Read-only is a supported posture, not a degraded one.

Any operation that mutates a system Dusk merely observes asks first, and a run that is aborted or fails leaves everything exactly as it was.

## 12. Plugins normalize, Dusk renders

Every transformation from a source's model into the Dusk shape happens in the plugin.
By the time data reaches the graph it is already final: refs canonical, kind resolved, names normalized.

Dusk never re-derives what a plugin settled.

Full reasoning: [ADR-0018](../adr/0018-normalization-at-the-edge.md).
