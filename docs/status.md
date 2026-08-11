# Status

What exists, what does not, and what is next.

Design is settled and recorded in [`adr/`](../adr/).
This tracks implementation against it.

**Keep this current in the same change that moves an item.** A status document that lags is worse than none, because it is read as truth.

## Legend

`[x]` built and tested · `[~]` partially built · `[ ]` not started

---

## Foundations

- [x] **`v1alpha1` plugin contract**: `dusk-plugin-sdk`, entity/relation/note/observation, plugin service, actions, config fields ([0002](../adr/0002-plugin-protocol.md), [0007](../adr/0007-entity-schema.md), [0015](../adr/0015-plugin-actions-and-events.md), [0023](../adr/0023-plugin-configuration.md))
- [x] **Conformance package**: batch validation, ref canonicalisation, config field validation ([0016](../adr/0016-plugin-sdk-repo.md))
- [x] **Release pipeline**: multi-arch image to GHCR, dispatch with scope and bump, dry run before tagging, untag on failure ([0021](../adr/0021-release-tooling.md))
- [x] **Helm chart**: `FetchHQ/charts`, publishes itself ([0019](../adr/0019-chart-repo.md), [0024](../adr/0024-charts-publishes-charts.md))
- [x] **CI**: lint, vet, no-cgo build, race tests on every PR and push ([0017](../adr/0017-engineering-policy.md))

## Onboarding

- [x] **GitHub App manifest flow**: `pkg/githubapp`, manifest rendering and code exchange ([0005](../adr/0005-github-app-and-access-modes.md))
- [x] **Redacting secret type**: `pkg/secret` ([0022](../adr/0022-credential-encryption.md))
- [x] **Envelope encryption**: `pkg/vault`, seal, open, and master key rotation ([0022](../adr/0022-credential-encryption.md))
- [x] **Boot configuration**: `internal/config`, fails closed without an encryption key ([0022](../adr/0022-credential-encryption.md))
- [x] **Credential store**: `internal/store`, encrypted at rest, atomic writes ([0022](../adr/0022-credential-encryption.md))
- [x] **Setup handlers**: first-boot detection, manifest POST page, callback with replay protection, install redirect
- [x] **`cmd/dusk`**: `serve`, `genkey`, structured logs, graceful shutdown
- [x] **Least-privilege manifest**: permissions generated per chosen access mode ([0005](../adr/0005-github-app-and-access-modes.md))
- [x] **Webhook receiver**: HMAC validation, replay rejection, body cap ([0006](../adr/0006-reconcile-triggering.md))

## Core

- [ ] **Reconciler**: `reconcile(ref)` reading `dusk.md` into a graph ([0001](../adr/0001-git-as-source-of-truth.md), [0004](../adr/0004-dusk-md-convention.md))
- [ ] **Storage**: SQLite, ref as a column, FTS5 ([0008](../adr/0008-storage.md))
- [ ] **Entity graph**: relations, traversal, drift between declared and observed ([0007](../adr/0007-entity-schema.md))
- [ ] **Poll floor**: periodic `git ls-remote` reconcile ([0006](../adr/0006-reconcile-triggering.md))
- [ ] **Sync observability**: last reconcile, what changed, what failed, what is stale

## Write path

- [ ] **Proof tokens**: issue on read, validate on write, actionable rejections ([0009](../adr/0009-proof-tokens.md))
- [ ] **Commit queue**: one commit per call on a per-session branch, with a flush policy ([0010](../adr/0010-mcp-surface.md))
- [ ] **Access modes**: read, proposal, and write ([0005](../adr/0005-github-app-and-access-modes.md))
- [ ] **Note dedup**: content hash and similarity warning ([0010](../adr/0010-mcp-surface.md))
- [ ] **Vocabulary**: `getKinds`, `mintKind`, proof token, fuzzy matching ([0007](../adr/0007-entity-schema.md))

## MCP

- [ ] **Read tools**: `search`, `get`, `neighbors`, `changes` ([0010](../adr/0010-mcp-surface.md))
- [ ] **Write tools**: `declare`, `note`, `relate`, `mintKind`, `push` ([0010](../adr/0010-mcp-surface.md))
- [ ] **Context injection**: `instructions`, `dusk_context`, client hook ([0014](../adr/0014-agent-context-injection.md))

## UI

- [ ] **HTTP API**: the UI is an ordinary client of it, with no privileged path
- [ ] **React app**: embedded via `go:embed`, dark only, Dracula Pro
- [ ] **Pages**: blocks as queries, entity pages and portal pages ([0013](../adr/0013-layout-and-pages.md))
- [ ] **PR previews**: render at an unmerged ref, semantic diff, comment bot ([0001](../adr/0001-git-as-source-of-truth.md))
- [ ] **Viewing auth**: GitHub OAuth against repo access ([0012](../adr/0012-viewing-auth.md))
- [ ] **Admin**: plugin configuration forms, sensitive fields write-only ([0023](../adr/0023-plugin-configuration.md))
- [ ] **Responsive layouts**: table to card, graph to list, diff to unified ([0025](../adr/0025-responsive-ui.md))
- [ ] **Viewport matrix tests**: five viewports, no overflow, touch target minimums ([0025](../adr/0025-responsive-ui.md))

## Plugins

- [ ] **Host runtime**: subprocess lifecycle, Tier 1 stdout and Tier 2 gRPC ([0002](../adr/0002-plugin-protocol.md))
- [ ] **Scheduler**: intervals, shared API budget, backoff, never delete on failure ([0011](../adr/0011-ingester-scheduling.md))
- [ ] **Kubernetes ingester**
- [ ] **Flux ingester**
- [ ] **GitHub ingester**
- [ ] **Plugin UI**: declarative view spec, then Web Components ([0020](../adr/0020-plugin-ui.md))
- [ ] **Actions**: invoke, dry run, classification, approval, events ([0015](../adr/0015-plugin-actions-and-events.md))

---

## Deployed

Running on mini-2 as of 2026-08-11.

- Image `ghcr.io/fetchhq/dusk`, multi-arch, public on GHCR.
- Chart `oci://ghcr.io/fetchhq/charts/dusk`, published by its own repository.
- Internal at `https://dusk.stout.zone`.
- Public webhooks at `https://dusk.theoutdoorprogrammer.com/webhooks`, forwarded by kube-repeater on mini-1. Only that prefix is exposed; the UI and setup stay on the LAN.
- Flux image automation is live and tracks stable tags, excluding prereleases.
- `DUSK_ENCRYPTION_KEY` is sealed with SOPS in the flux repo and held in 1Password.

## Next

1. Run onboarding against real GitHub, which is the only way to confirm the manifest flow behaves as [ADR-0005](../adr/0005-github-app-and-access-modes.md) assumes.
2. The reconciler over a single local repo, with no App, webhooks, or UI involved.
3. Storage and the graph.

## Known gaps

- Nothing keeps the chart in step with the application. A release adding a required value ships an image no published chart can deploy until someone notices ([0024](../adr/0024-charts-publishes-charts.md)).
- `internal/nextversion` exists but no release has been cut, so the pipeline is untested against a real tag.
- Onboarding has only been exercised against fakes. Nothing has talked to real GitHub yet.
- `dusk.theoutdoorprogrammer.com` does not resolve yet. The Cloudflare change is merged but its Spacelift stack has `auto_deploy = false` pending a state migration, so webhooks cannot arrive until that is applied.
- Deliveries are verified and acknowledged but not yet acted on, because the reconciler does not exist. The poll floor in [0006](../adr/0006-reconcile-triggering.md) means nothing is lost meanwhile.
