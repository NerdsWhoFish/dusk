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
- [x] **`declare`**: `internal/write`, routing to the file that declares an entity, rewriting only its frontmatter, and committing to the default branch. Creating places the file under an `include` glob and refuses when none would reach it ([0010](../adr/0010-mcp-surface.md))
- [x] **Write containment**: writes reach only repositories a sweep has seen *and* that already contain a `dusk.md`. Dusk never creates one, because that file is how a repository consents ([0004](../adr/0004-dusk-md-convention.md))
- [ ] **Proposal mode**: a per-session branch and a pull request. Write mode commits straight to the default branch and needs neither, so this is deferred until somebody runs in proposal mode ([0005](../adr/0005-github-app-and-access-modes.md), [0010](../adr/0010-mcp-surface.md))
- [ ] **Read mode**: return the proposed diff rather than failing, which is what makes read-only first class rather than broken ([0005](../adr/0005-github-app-and-access-modes.md))
- [x] **Notes**: a note is its own file, discovered like any other, written to the config repository's `.dusk/` ([0031](../adr/0031-notes-are-files.md)). `search` covers entities and notes together, and `get` returns the notes attached to what it answers about
- [ ] **Note dedup**: content hash and similarity warning ([0010](../adr/0010-mcp-surface.md))
- [ ] **Vocabulary**: `getKinds`, `mintKind`, proof token, fuzzy matching ([0007](../adr/0007-entity-schema.md))

## MCP

- [x] **Read tools**: `search`, `get`, `neighbors`, `changes` over streamable HTTP at `/mcp`, answering in markdown ([0010](../adr/0010-mcp-surface.md))
- [x] **Agent surface access**: bearer token, or an explicit trusted-network mode, and off until one of them says. Never a default ([0012](../adr/0012-viewing-auth.md))
- [ ] **Authorization derived from repository access**: an agent presents a GitHub token and sees only the repositories it can read. The index is already partitioned by repository, so this is a predicate rather than a permission model. Deferred until there is a second reader ([0012](../adr/0012-viewing-auth.md))
- [~] **Write tools**: `declare` and `note` are built, and every read issues the proof token they need. `relate`, `mintKind` and `push` are not ([0010](../adr/0010-mcp-surface.md))
- [~] **Context injection**: the MCP `instructions` field is served. `dusk_context` and the client hook are not built ([0014](../adr/0014-agent-context-injection.md))

## UI

- [x] **HTTP API**: `/api/search`, `/api/entities`, `/api/entities/{ref}`, `/api/overview`, `/api/status`. The UI is an ordinary client of it with no privileged path
- [x] **React app**: React 19 and TypeScript, built by Vite, embedded via `go:embed`, dark only, Dracula Pro
- [x] **Browser auth**: a session cookie exchanged for the same token agents present, so one policy covers both surfaces
- [~] **Pages**: entity pages and a portal landing composed of blocks are built. The blocks are fixed rather than declared as queries in a markdown file, which is what [0013](../adr/0013-layout-and-pages.md) actually asks for
- [x] **PR previews**: the catalog rendered at an unmerged ref, a semantic diff, and one comment edited in place ([0001](../adr/0001-git-as-source-of-truth.md), [0037](../adr/0037-pull-request-previews.md))
- [x] **Viewing auth**: sign in with GitHub, visibility derived from repository access, observed entities hidden unless allowed ([0012](../adr/0012-viewing-auth.md), [0036](../adr/0036-deriving-what-a-viewer-sees.md))
- [ ] **Admin**: plugin configuration forms, sensitive fields write-only ([0023](../adr/0023-plugin-configuration.md))
- [~] **Responsive layouts**: mobile first, one breakpoint, wide content scrolls inside its own container. Nothing yet does the table-to-card or graph-to-list transform, because neither exists ([0025](../adr/0025-responsive-ui.md))
- [ ] **Viewport matrix tests**: five viewports, no overflow, touch target minimums. **The matrix has never been run**: neither browser tool available could actually change the viewport, so the responsive layout is written but unverified ([0025](../adr/0025-responsive-ui.md))

## Plugins

- [ ] **Host runtime**: subprocess lifecycle, Tier 1 stdout and Tier 2 gRPC ([0002](../adr/0002-plugin-protocol.md))
- [~] **Scheduler**: intervals, concurrency cap, exponential backoff and a circuit breaker, never delete on failure ([0011](../adr/0011-ingester-scheduling.md)). The shared per-source API budget is not built: each ingester is only bounded by its own interval
- [x] **Kubernetes ingester**: nodes and services per cluster, in tree ahead of the plugin protocol ([0034](../adr/0034-ingesters-in-tree-first.md))
- [x] **Drift**: declared against observed, matched through `observed_as` ([0013](../adr/0013-layout-and-pages.md))
- [ ] **Flux ingester**
- [ ] **GitHub ingester**
- [ ] **Plugin UI**: declarative view spec, then Web Components ([0020](../adr/0020-plugin-ui.md))
- [ ] **Actions**: invoke, dry run, classification, approval, events ([0015](../adr/0015-plugin-actions-and-events.md))

---

## Next

1. Notes, which need a home in `dusk.md` before `search` can cover them alongside entities. They are what agents write most, so this is the write path's other half.
2. Pull request previews, which the ref-keyed index was built for and nothing yet uses.
3. The HTTP API and the UI, which is the first surface a human rather than an agent reads.

## Known gaps

- Catalog content is fed to agents with no trust boundary of its own. [ADR-0030](../adr/0030-account-allowlist.md) narrows *who* can reach that path but does nothing about a compromised repository inside an allowed account.
- Two repositories declaring the same entity is **reported but not resolved** ([0033](../adr/0033-graph-integrity.md)). `integrity` names both declarations; `Get` still returns whichever sorts first until a human picks.
- The MCP surface has no authentication. Anyone able to reach the private host can read the whole catalog ([0012](../adr/0012-viewing-auth.md)).
- Nothing keeps the chart in step with the application. A release adding a required value ships an image no published chart can deploy until someone notices ([0024](../adr/0024-charts-publishes-charts.md)).
- Access mode is fixed at registration. Changing it means editing the App's permissions on GitHub, which installations must then approve.
- Notes are ranked by pinned-then-id. [0031](../adr/0031-notes-are-files.md) wants kind to drive ranking so a gotcha outranks a todo without being pinned by hand.
- A note's refs and a relation's target are still unchecked at write time, deliberately, because the target may live in a repository Dusk cannot see. Both are now **reported** by `integrity` instead of failing silently ([0033](../adr/0033-graph-integrity.md)).
- Integrity reports every unresolvable ref, and in a partially adopted catalog most of them are legitimate. Nothing distinguishes "typo" from "not adopted yet".
- Drift only matches a declaration to an observation through an explicit `observed_as`. Nothing infers the mapping, so an estate that has not written them shows every entity on both sides of the report.
- Ingested entities are stored under a reserved `ingester:` scope that occupies the repository slot. Anything treating a repository as clonable has to check `index.IsObserved` first.
- A rate limit is recognised and logged but not waited out. A sweep that exhausts the budget gives up until the next one rather than resuming when the limit resets.
- The UI's responsive behaviour is unverified. It is written mobile first against [0025](../adr/0025-responsive-ui.md)'s rules, but the viewport matrix has never actually been run against it.
- A browser session is a bearer token in a cookie by another name: it grants the whole catalog and cannot be revoked short of rotating `DUSK_MCP_TOKEN`, which signs it. Signing in with GitHub narrows a view; it does not close this path ([0036](../adr/0036-deriving-what-a-viewer-sees.md)).
- A restricted viewer's drift, integrity and kind counts are not filtered, so those blocks can count things the viewer cannot open.
- OAuth sign-in requests `repo` scope, which is far more than listing repository names needs. GitHub offers nothing narrower that still sees private repositories.
- Identity sessions live in memory, so a restart signs everybody out.
- Every open pull request holds a partition of the index, rebuilt on every push to its branch. Nothing prunes an abandoned one until it closes ([0037](../adr/0037-pull-request-previews.md)).
- A preview's drift and integrity are unfiltered for a restricted viewer, so those counts can include entities they cannot open.
