# Status

What exists, what does not, and what is next.

Design is settled and recorded in [`adr/`](../adr/).
This tracks implementation against it.

**Keep this current in the same change that moves an item.** A status document that lags is worse than none, because it is read as truth.

This tracks the product. It deliberately says nothing about any particular deployment.

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
- [x] **Verified against real GitHub**: an App was registered through the manifest flow, credentials exchanged and stored encrypted, and signed `ping` and `installation` deliveries were verified and accepted. The onboarding path is proven, not just tested.

## Core

- [x] **`dusk.md` schema and parser**: `pkg/duskmd`, one entity per file with its prose as the description, derived refs, outbound relations only, and includes that cannot escape the repository ([0004](../adr/0004-dusk-md-convention.md), [0026](../adr/0026-dusk-md-schema.md))
- [x] **Reconciler**: `internal/reconcile`, expanding includes against a tree into the graph, with reading split from storing so a checkout can be validated with no index. `dusk validate` is the local command ([0001](../adr/0001-git-as-source-of-truth.md), [0004](../adr/0004-dusk-md-convention.md))
- [x] **Source boundary**: no VCS type reaches the reconciler, and the local directory source refuses a path that leaves it ([0005](../adr/0005-github-app-and-access-modes.md))
- [x] **GitHub source**: `githubapp.Repository`, reading over the API at a commit resolved once, with a tree cache that needs no invalidation ([0029](../adr/0029-reading-repositories.md))
- [x] **Installation auth**: App assertions and installation tokens, reused until close to expiry ([0005](../adr/0005-github-app-and-access-modes.md))
- [x] **Storage**: `internal/index`, SQLite partitioned by repository and git ref, FTS5 search with ranking and snippets, transactional replace, cheap per-ref garbage collection ([0008](../adr/0008-storage.md))
- [~] **Entity graph**: relations and inbound traversal to a bounded depth are built. Drift between declared and observed waits on there being an observed side ([0007](../adr/0007-entity-schema.md))
- [x] **Poll floor**: periodic sweep of every permitted installation, running whether or not webhooks are configured ([0006](../adr/0006-reconcile-triggering.md))
- [x] **Webhook triggering**: a push reconciles one repository, an installation change triggers a sweep, both answered before the work runs ([0006](../adr/0006-reconcile-triggering.md))
- [x] **Account allowlist**: only the App's own account by default, checked on both the sweep and the delivery ([0030](../adr/0030-account-allowlist.md))
- [x] **Sync observability**: per-repository status with commit, counts, and last error, surfaced to agents through the MCP `changes` tool. No human-facing surface yet

## Write path

- [x] **Proof tokens**: `pkg/proof`, issued by a read, invalidated by change with a TTL backstop, rejections naming the call that fixes them ([0009](../adr/0009-proof-tokens.md))
- [x] **Rendering `dusk.md`**: `pkg/duskmd`, frontmatter rewritten and prose left byte-identical, so a write cannot disturb what somebody wrote ([0026](../adr/0026-dusk-md-schema.md))
- [x] **Committing over the API**: `githubapp.CommitFile`, one file per commit, presenting the blob sha so a raced write collides instead of overwriting ([0010](../adr/0010-mcp-surface.md), [0029](../adr/0029-reading-repositories.md))
- [ ] **`declare`**: the tool that ties the three together. Creating an entity means a new file, which is only read if an `include` glob catches it, so `declare` must place it or extend the root's `include`
- [ ] **Proposal mode**: a per-session branch and a pull request. Write mode commits straight to the default branch and needs neither, so this is deferred until somebody runs in proposal mode ([0005](../adr/0005-github-app-and-access-modes.md), [0010](../adr/0010-mcp-surface.md))
- [ ] **Read mode**: return the proposed diff rather than failing, which is what makes read-only first class rather than broken ([0005](../adr/0005-github-app-and-access-modes.md))
- [ ] **Notes have no home in `dusk.md`**: deliberately deferred by [0026](../adr/0026-dusk-md-schema.md) until the MCP write path that authors them exists. Until then `search` covers entities alone, where [0010](../adr/0010-mcp-surface.md) wants entities and notes together
- [ ] **Note dedup**: content hash and similarity warning ([0010](../adr/0010-mcp-surface.md))
- [ ] **Vocabulary**: `getKinds`, `mintKind`, proof token, fuzzy matching ([0007](../adr/0007-entity-schema.md))

## MCP

- [x] **Read tools**: `search`, `get`, `neighbors`, `changes` over streamable HTTP at `/mcp`, answering in markdown ([0010](../adr/0010-mcp-surface.md))
- [x] **Agent surface access**: bearer token, or an explicit trusted-network mode, and off until one of them says. Never a default ([0012](../adr/0012-viewing-auth.md))
- [ ] **Authorization derived from repository access**: an agent presents a GitHub token and sees only the repositories it can read. The index is already partitioned by repository, so this is a predicate rather than a permission model. Deferred until there is a second reader ([0012](../adr/0012-viewing-auth.md))
- [ ] **Write tools**: `declare`, `note`, `relate`, `mintKind`, `push` ([0010](../adr/0010-mcp-surface.md))
- [~] **Context injection**: the MCP `instructions` field is served. `dusk_context` and the client hook are not built ([0014](../adr/0014-agent-context-injection.md))

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

## Next

1. The write path: proof tokens, the commit queue, and the MCP write tools. This is the half that makes the catalog maintain itself.
2. Notes, which need a home in `dusk.md` before `search` can cover them alongside entities.
3. Pull request previews, which the ref-keyed index was built for and nothing yet uses.

## Known gaps

- Catalog content is fed to agents with no trust boundary of its own. [ADR-0030](../adr/0030-account-allowlist.md) narrows *who* can reach that path but does nothing about a compromised repository inside an allowed account.
- Two repositories declaring the same entity is undetected. Within one repository it is an error; across repositories the graph keeps both and a read returns whichever sorts first.
- The MCP surface has no authentication. Anyone able to reach the private host can read the whole catalog ([0012](../adr/0012-viewing-auth.md)).
- Nothing keeps the chart in step with the application. A release adding a required value ships an image no published chart can deploy until someone notices ([0024](../adr/0024-charts-publishes-charts.md)).
- Access mode is fixed at registration. Changing it means editing the App's permissions on GitHub, which installations must then approve.
