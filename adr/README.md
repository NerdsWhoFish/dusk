# Architecture Decision Records

Decisions are recorded as [MADR](https://adr.github.io/madr/) records: context, considered options, decision, and consequences both good and bad.

**The value is the rejected alternatives and the reasoning**, not the decision itself.
That is what stops the same argument happening again in six months.

## Index

| # | Decision | In one line |
| --- | --- | --- |
| [0001](0001-git-as-source-of-truth.md) | Git is the source of truth, index keyed by ref | Entities are markdown in the repos they describe; the index is derived and rebuildable, and `reconcile(ref)` is what makes PR previews nearly free |
| [0002](0002-plugin-protocol.md) | Plugins are subprocesses with a published contract | One proto schema, two transports: protojson on stdout for ingesters, gRPC for interactive plugins. Explicitly not `hashicorp/go-plugin` |
| [0003](0003-license.md) | Apache 2.0 with a DCO | Patent grant for corporate adopters; DCO rather than a CLA because relicensing optionality is the thing being given up on purpose |
| [0004](0004-dusk-md-convention.md) | A repo opts in by containing `dusk.md` | One well-known file is the sole entry point. No repo is crawled without consent, and write routing falls out for free |
| [0005](0005-github-app-and-access-modes.md) | GitHub App via the manifest flow, three access modes | Dusk registers its own App so there is no PAT path; read, proposal, and write are the modes |
| [0006](0006-reconcile-triggering.md) | Webhook-triggered reconcile with a poll floor | The poll floor is load-bearing and must not be removed as redundant: webhooks are lost in normal operation and a pure-push system goes silently stale |
| [0007](0007-entity-schema.md) | Small schema, open kinds, attributes escape hatch | Entity, Relation, Note, Observation. Declaration and observation are different layers, and divergence is drift rather than conflict |
| [0008](0008-storage.md) | SQLite via GORM on a pure-Go driver | Refs as a column, FTS5 for search. The driver choice is load-bearing: the default GORM driver requires cgo |
| [0009](0009-proof-tokens.md) | Proof tokens gate every write | You cannot write what you have not read. One invariant replaces dedup, optimistic concurrency, and the vocabulary gate |
| [0010](0010-mcp-surface.md) | A few fat MCP tools, not a schema mirror | Seven tools, per-session commit branches, note kinds, and three layers of note dedup |
| [0011](0011-ingester-scheduling.md) | Scheduling, shared API budget, never delete on failure | A failing ingester marks observations stale and never removes entities. "I could not look" is never "it is not there" |
| [0012](0012-viewing-auth.md) | Viewing authorization derives from repo access | No second permission model to drift. Per-entity filtering falls out for free |
| [0013](0013-layout-and-pages.md) | Pages are markdown, blocks are queries | Entity pages belong to satellite repos, portal pages to the config repo. Route collisions are impossible by construction |
| [0014](0014-agent-context-injection.md) | Context injected three ways, scoped to Dusk | MCP `instructions` is emitted before roots arrive, so it is location-blind; `dusk_context` and a hook cover the rest |
| [0015](0015-plugin-actions-and-events.md) | Entity-scoped actions, events not notes | Default deny, required dry run, proof tokens on mutations. This is where Dusk becomes a privileged control plane |
| [0016](0016-plugin-sdk-repo.md) | The plugin contract lives in its own repo | The `.proto` first, the Go SDK second, so non-Go authors are first-class |
| [0017](0017-engineering-policy.md) | Engineering policy for FetchHQ repositories | Go conventions, no cgo without an ADR, package layout, DRY and its limit, docs everywhere, and the testing rules |
| [0018](0018-normalization-at-the-edge.md) | Plugins normalize, Dusk never re-derives | Data is final by the time it reaches the graph, which is what keeps renderers free of a branch per source |
| [0019](0019-chart-repo.md) | The Helm chart lives in its own repository | `FetchHQ/charts`, published as OCI. Release coordination superseded by 0024 |
| [0020](0020-plugin-ui.md) | Plugins contribute UI as Web Components, in three tiers | Declarative spec by default, Web Component opt-in, iframe for the rest. Never compiled in, because that is the Backstage fork |
| [0021](0021-release-tooling.md) | Release with docker buildx and helm, not GoReleaser | Dusk ships an image and a chart, none of the artifacts GoReleaser exists for. All the release safety patterns are kept |
| [0022](0022-credential-encryption.md) | Credentials encrypted at rest with a required external key | No unencrypted mode. Envelope encryption so the key can rotate, and the chart never generates it |
| [0023](0023-plugin-configuration.md) | Plugins declare typed config; sensitive values never enter git | Non-sensitive config is markdown in the config repo, secrets are referenced from the encrypted store and are write-only |
| [0024](0024-charts-publishes-charts.md) | The charts repository publishes its own charts | Supersedes half of 0019. Chart version and appVersion are independent, so no App and no cross-repo write |
| [0025](0025-responsive-ui.md) | Mobile and desktop are both first class | A fixed viewport matrix is the definition, tests assert no overflow and touch target size, screenshot diffing is rejected |
| [0026](0026-dusk-md-schema.md) | One file declares one entity, and its prose is the description | Fills in what 0004 left open. Refs are derived not authored, only outbound relations are declarable, `include` is one level deep and cannot escape the repository. Also places the declaration path's normalization edge, which 0018 left unstated, and spends the repository's zero-dependency property on a YAML parser |

| [0027](0027-design-target.md) | The design target is a single operator and their agents | Homelabs and personal infrastructure, not platform teams. Team use stays supported but never breaks a tie. Corrects an audience assumption 0003, 0005 and 0013 had absorbed from Backstage |

| [0028](0028-amending-adrs.md) | ADRs are amended in place, and retired rather than deleted | Any part may be corrected where it stands, disclosed in a dated section; a wholesale rewrite supersedes instead. A dead decision is `Retired`, and no ADR is ever deleted |

| [0029](0029-reading-repositories.md) | Repositories are read over the API, at a pinned commit | Three endpoints instead of a clone, because a reconcile reads one file per repository. The ref is resolved once so a moving branch cannot stitch two commits into one graph, which also makes the tree cache safe |

## Writing one

Number sequentially and never reuse a number.

Any part of an ADR may be amended in place, recorded in a dated `## Amendments` section at the bottom of the file saying what changed and why.
The limit is scale rather than category: a wholesale rewrite of the major sections is a **new** ADR that supersedes the old one, and the old one stays with its status updated.

A decision that no longer governs anything is marked **`Retired`**, with a dated entry saying what happened.
It keeps its number and its file. An ADR is never deleted, because its rejected alternatives are the reason nobody should re-propose what it rejected.

Full policy in [ADR-0028](0028-amending-adrs.md).

Every ADR states its rejected options and why they lost, and its consequences honestly, including the bad ones.
An ADR with no downsides listed has not been thought through.

Any rule an ADR states as load-bearing gets a test named after it, per [ADR-0017](0017-engineering-policy.md):

```text
TestADR0011_FailedIngestDoesNotDelete
```

**Adding, superseding, or retiring an ADR updates this index in the same change.**
An index that lies is worse than no index, because it sends readers to decisions that do not exist.
