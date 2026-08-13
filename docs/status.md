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
- [x] **Helm chart**: `NerdsWhoFish/charts`, publishes itself ([0019](../adr/0019-chart-repo.md), [0024](../adr/0024-charts-publishes-charts.md))
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
- [x] **Entity graph**: relations and inbound traversal to a bounded depth, and drift between the declared and observed halves now that plugins supply the observed one ([0007](../adr/0007-entity-schema.md))
- [x] **Poll floor**: periodic sweep of every permitted installation, running whether or not webhooks are configured ([0006](../adr/0006-reconcile-triggering.md))
- [x] **Webhook triggering**: a push reconciles one repository, an installation change triggers a sweep, both answered before the work runs ([0006](../adr/0006-reconcile-triggering.md))
- [x] **Account allowlist**: only the App's own account by default, checked on both the sweep and the delivery ([0030](../adr/0030-account-allowlist.md))
- [x] **Sync observability**: per-repository status with commit, counts, and last error, through the MCP `changes` tool and the homepage's `reads` block

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

- [x] **Read tools**: `search`, `get`, `neighbors`, `changes`, `drift` and `dusk_context` over streamable HTTP at `/mcp`, answering in markdown ([0010](../adr/0010-mcp-surface.md))
- [x] **Agent surface access**: bearer token, or an explicit trusted-network mode, and off until one of them says. Never a default ([0012](../adr/0012-viewing-auth.md))
- [ ] **Authorization derived from repository access**: an agent presents a GitHub token and sees only the repositories it can read. The index is already partitioned by repository, so this is a predicate rather than a permission model. Deferred until there is a second reader ([0012](../adr/0012-viewing-auth.md))
- [~] **Write tools**: `declare`, `note` and `page` are built, and every read issues the proof token they need. `relate`, `mintKind` and `push` are not ([0010](../adr/0010-mcp-surface.md))
- [x] **Curating the homepage**: `page` reads the declared page, or the default written out when there is none, and rewrites it against a proof token. One tool for both halves, because the read is what authorizes the write and a separate read tool would look optional. The page is parsed before it is committed, so a bad block is a failed call rather than a blank homepage ([0013](../adr/0013-layout-and-pages.md))
- [~] **Context injection**: the MCP `instructions` field is served and `dusk_context` answers with the estate. The client hook is not built ([0014](../adr/0014-agent-context-injection.md))

## UI

- [x] **HTTP API**: search, entities and their dependents, overview, status, drift, integrity, home, viewer, diff, and the plugin routes. The UI is an ordinary client of it with no privileged path
- [x] **React app**: React 19 and TypeScript, built by Vite, embedded via `go:embed`, dark only, Dracula Pro
- [x] **Browser auth**: a session cookie exchanged for the same token agents present, so one policy covers both surfaces
- [x] **Pages**: the homepage is `.dusk/home.md` in the config repository, an ordered list of typed blocks, each a query rather than a widget. What is declared is the whole page, so removing a default block is deleting a line. Entities blocks take the query grammar including `related:` in either direction and sorting by any attribute, which is what makes "the latest three flights" a block. Blocks resolve server side, so a page is one request rather than one per block ([0013](../adr/0013-layout-and-pages.md), [0035](../adr/0035-blocks-resolve-server-side.md)). Documented in [docs/pages.md](pages.md)
- [x] **PR previews**: the catalog rendered at an unmerged ref, a semantic diff, and one comment edited in place ([0001](../adr/0001-git-as-source-of-truth.md), [0037](../adr/0037-pull-request-previews.md))
- [x] **Viewing auth**: sign in with GitHub using the App Dusk registered, visibility derived from repository access, observed entities hidden unless allowed ([0012](../adr/0012-viewing-auth.md), [0036](../adr/0036-deriving-what-a-viewer-sees.md))
- [x] **Admin**: the plugins page installs, updates, uninstalls and configures, with sensitive fields write-only and a marker saying which are set so an empty box is not mistaken for an empty value ([0023](../adr/0023-plugin-configuration.md)). It is also where an action is enabled, where a plugin's own views mount, where its output is read, and where what has been run is listed
- [x] **Actions in the browser**: a card per enabled action, a form rendered from its JSON Schema, a preview button, and a confirmation for anything destructive that carries the preview or says there is none. Nothing in it knows about any plugin. The entity read issues the proof token the action presents, so a page left open while something changed refuses rather than acting on what is no longer true ([0009](../adr/0009-proof-tokens.md))
- [~] **Responsive layouts**: mobile first, one breakpoint, wide content scrolls inside its own container. Nothing yet does the table-to-card or graph-to-list transform, because neither exists ([0025](../adr/0025-responsive-ui.md))
- [~] **Viewport matrix tests**: run by hand at 320, 390, 430, 768, 1024 and 1440 against a real catalog, on the landing and entity pages. No overflow and no touch target under 44px at any of them. Not automated, so nothing stops the next change regressing it ([0025](../adr/0025-responsive-ui.md))

## Plugins

- [x] **One transport**: gRPC over a host-provided unix socket, and no second way in ([0039](../adr/0039-one-plugin-transport.md))
- [x] **`GetAsset` RPC**: streams a plugin's JavaScript in chunks, which Dusk hashes and serves content addressed at `/plugin-assets/{plugin}/{sha}.js` from its own origin. Immutable caching is safe there because a different asset has a different URL. Proven by `dusk-plugin-airtrail`
- [x] **Plugin UI**: a plugin ships a custom element, not a React component, so it shares no runtime with Dusk and brings its own rendering. Styling crosses the shadow boundary through CSS custom properties, which inherit where classes do not ([0020](../adr/0020-plugin-ui.md))
- [x] **Scheduler**: intervals, concurrency cap, exponential backoff and a circuit breaker, never delete on failure ([0011](../adr/0011-ingester-scheduling.md))
- [x] **Shared per-source budget**: a plugin names the config fields identifying its upstream system, and every configuration resolving to the same values queues behind the others rather than each assuming it has the whole quota. Declaring no key fields means no sharing, so a plugin that has not thought about it is not throttled by accident. A run that cannot get a turn is **deferred, never failed**: counting a deferral as a failure would let a busy source trip the circuit breaker meant for a broken one. Dusk meters runs rather than requests, because what a plugin does upstream happens inside its own process ([0011](../adr/0011-ingester-scheduling.md))
- [x] **Kubernetes moved out of core**: `internal/ingest/kubernetes.go`, `DUSK_KUBERNETES` and the cluster configuration are gone, and `k8s.io/client-go` with them. Core now carries no ingester at all: the scheduler is constructed empty and plugins join it as they start, so there is no in-tree path an observation can take ([0034](../adr/0034-ingesters-in-tree-first.md), [0040](../adr/0040-core-and-plugins.md))
- [x] **Kubernetes plugin**: `NerdsWhoFish/dusk-plugin-kubernetes` serves `PluginService` over a host-provided unix socket, observes a real cluster, and ports the namespace and plumbing filters intact
- [x] **Drift**: declared against observed, matched through `observed_as`, and limited to kinds something actually watches ([0038](../adr/0038-what-drift-may-say.md))
- [x] **Declarative view spec**: [0020](../adr/0020-plugin-ui.md)'s Tier 1. A plugin declares a layout, the fields to show and what to say when there are none, and Dusk's own React renders it with no JavaScript from the plugin and therefore no trust decision. A contribution sets a spec or an element, never both. Tier 2 was still built first, because it is what the first plugin with a view needed
- [x] **A view can mount on the plugin's own page**: a contribution names its slot, so something that is about the plugin rather than about one entity has somewhere to go. Creating a thing the catalog has never seen has no entity to mount against
- [x] **Actions**: declared, denied by default, enabled by a deliberate act, classified, previewed, approved, invoked, and recorded as events ([0015](../adr/0015-plugin-actions-and-events.md)). One declaration is a button on the entity page, an `invoke` over MCP, and the approval gate. Routing is by **which plugin observed the entity**, and an action runs against the configuration of the instance that saw it, so a plugin watching two clusters acts on the right one. A mutating action asks its instance to observe again rather than leaving the catalog showing what was true before
- [x] **Host runtime**: a plugin is exec'd on a host-provided unix socket, answers `Describe`, and joins the rotation as an ordinary `ingest.Ingester`, so scheduling, backoff and the never-delete rule are not reimplemented for it
- [x] **Marketplace, install and update**: `dusk-plugin-*` in allowlisted orgs, checksum verified before anything runs, cached on disk so a restart needs no network, updates applied only when a human says ([0042](../adr/0042-installing-plugins.md))
- [x] **Configuration in the UI**: a form rendered from the plugin's declared `config_fields`, so Dusk knows nothing about any plugin ([0023](../adr/0023-plugin-configuration.md))
- [x] **Configuration splits on sensitivity**: `sensitive` was a form hint and nothing else, so every plugin credential sat in readable JSON on the volume and was returned by the plugins API to anything holding the token. The plain half stays in the record; the sensitive half is sealed under the master key beside it, reaches the plugin only over its own socket, and is never read back. A field submitted empty keeps what is stored, because a write-only form submits empty when it was not retyped; an explicit null forgets it. A credential an older Dusk wrote in the clear moves on the next start ([0022](../adr/0022-credential-encryption.md), [0023](../adr/0023-plugin-configuration.md))
- [x] **Abandoned observations are reported and forgettable**: moving an in-tree ingester to a plugin renames its scope, and the old one keeps answering with entities nothing refreshes, so every ref reads as declared twice. `integrity` names them and a button clears them. Never automatic: an ingester that is merely unconfigured for a while would otherwise lose the history it is about to re-observe ([0011](../adr/0011-ingester-scheduling.md))
- [x] **Instances**: one plugin, several configurations, each with its own scope and its own place in the rotation. One Kubernetes plugin observes one cluster, so a second cluster is a second instance rather than a second install. They share a process and fail apart
- [x] **Failure is visible**: the rotation already knew an instance was failing and only the log said so, so a plugin whose every run errored still read as "running" in the UI. Each instance now reports its last error and consecutive failure count, the row says `failing` and shows the error, and a plugin failing on every instance says so at the plugin. The scheduler was the source of truth all along; nothing new is recorded, it is only surfaced
- [x] **A plugin's view on the homepage**: a `view` block names the plugin and either the ref to mount against or a query to resolve, so a page can ask its own question and let the plugin decide only how the answer looks. Results reach the element as a property rather than an attribute, because stringifying a result set into markup would be absurd. A plugin contributing several elements is disambiguated by `element`, and a block that names none of them says so and lists them rather than picking one ([0020](../adr/0020-plugin-ui.md))
- [x] **Plugin capability over MCP**: one `invoke` tool, and discovery folded into `get`, so a tenth plugin adds no tools. An agent reads an entity and the answer already says what can be done to it, with which class, whether it needs confirming, and which read yields the proof token ([0041](../adr/0041-plugins-reach-agents-as-actions.md))
- [x] **Configuring a plugin over MCP**: a `configure` tool, merging over what is there rather than replacing it, so setting one field does not clear the rest. Sensitive fields are **refused by name**, not accepted and dropped. ADR-0041 said this would ride on `declare`, on the premise that non-sensitive configuration was already frontmatter in the config repository; it never was, and [0043](../adr/0043-plugin-configuration-stays-out-of-git.md) records why it stays where it is ([0041](../adr/0041-plugins-reach-agents-as-actions.md), [0023](../adr/0023-plugin-configuration.md))
- [x] **Plugin health reaches an agent**: `changes` answers "how much should I trust this", and an observation nothing refreshes is a stale answer with no marker on it, so a failing plugin belongs in that answer rather than only on a page

### Plugins composing, through Dusk

One plugin's work often wants another's: create a DNS record, then restart the workload that serves it. The sockets make direct calls physically possible, and every one of these items exists because they should not be made that way.

A plugin calling another directly would collapse the trust boundary, since approving one plugin would silently grant it everything its neighbours can do. It would create an undeclared dependency graph with no versioning and a silent skip when the other plugin is absent, which is the failure mode [0011](../adr/0011-ingester-scheduling.md) exists to prevent. And it would be invisible to the catalog, which is self-defeating for a product whose claim is that it records what happened.

Dusk is the orchestrator. An agent already composes actions this way through `invoke`, so what follows is about making it declarative, guarded and recorded rather than possible.

- [x] **Plugins cannot serve each other**: Dusk mints a token per start, presents it on every call, and a plugin refuses anything without it. Not a defence against a hostile plugin, which already runs with Dusk's permissions ([0042](../adr/0042-installing-plugins.md)); it stops plugins coupling to each other by accident
- [x] **Declared composition**: an action declares what may follow it, by ref rather than by reaching for a socket, and an invocation returns the concrete steps once it knows them. Dusk resolves, approves and invokes each. **Only an action the descriptor already declared is accepted**, because otherwise approving a chain would mean approving whatever the plugin appended once it was running
- [x] **Approval across a chain**: approval is the strongest `ActionClass` anywhere in the declared chain, so a harmless first step leading to a destructive one is confirmed like the destructive one ([0015](../adr/0015-plugin-actions-and-events.md))
- [x] **A missing link is loud**: a step nothing serves, or one whose action is not enabled, is reported as a failed step naming what is absent, and the chain stops there rather than running the rest of a sequence whose earlier step did not happen
- [x] **Events span the chain**: every invocation in one composition carries the same chain id, so what actually ran is answerable afterwards rather than being adjacent records
- [x] **Stronger isolation than a shared token, decided against**: a socketpair on an inherited descriptor would remove the path, and would re-decide [0039](../adr/0039-one-plugin-transport.md) by the back door, because gRPC on a descriptor is comfortable only in Go and a socket address is what every language handles. Per-plugin directories change nothing under one user, and unlinking after connect turns a reconnect into a permanent failure. The token stays the boundary ([0044](../adr/0044-plugins-keep-the-socket-directory.md))
- [x] **Shared plugin release workflow**: GoReleaser with conventional-commit notes, living once in `NerdsWhoFish/.github` and called by every plugin rather than copied ([0021](../adr/0021-release-tooling.md))

GitHub is not on this list. It is core and stays there: git is the source of truth, so GitHub is substrate rather than a source among sources ([0040](../adr/0040-core-and-plugins.md)).

### Planned plugins

Written down so the order is a choice rather than whatever comes to mind next. Kubernetes was first because it already worked in tree, so the protocol was measured against real behaviour rather than a guess. AirTrail was second because it is small enough to prove a plugin written from nothing.

**Observe only**, at least to begin with:

- [x] **AirTrail**: flights and the airports they connect, in `dusk-plugin-airtrail`. The first plugin to ship a **UI contribution**, so it is what proves [0020](../adr/0020-plugin-ui.md) end to end: `GetAsset` streams the element's JavaScript, Dusk serves it content addressed from its own origin, and the entity page mounts the tag
- [x] **Music Assistant**: players and their groups, in `dusk-plugin-music-assistant`. WebSocket rather than REST, because it has no REST, and authentication is the first message rather than a header. Its actions are declared and `DryRun` works; `Invoke` deliberately does not, because a plugin that can make noise in somebody's house should not be able to on its first version
- [ ] **Flux**: what GitOps believes is deployed, against what Kubernetes reports running. The pair is drift with a cause attached
- [ ] **OCI registries**: Harbor, GHCR and friends: images, tags and what is actually pulled
- [ ] **Firewalla**: the network layer under everything else: devices, rules, what is reachable
- [ ] **Karakeep**: bookmarks and reading as catalog notes
- [ ] **Perplexity**: research answers attached to the entity they are about

**Observe and act.** These are what make [0015](../adr/0015-plugin-actions-and-events.md) load-bearing rather than speculative, and why every one of them will need `ACTION_CLASS_DESTRUCTIVE` to mean something:

- [ ] **Spacelift**: stacks, runs and their state, and creating or managing them rather than only reporting
- [ ] **ADRs**: decision records as first-class entities, with authoring, superseding and retiring as actions. This repository's own ADR conventions are enforced by hand today, so it would be its own first user
- [ ] **Claude**: sessions and their work as catalog history
- [ ] **Home Assistant**: entities, automations and the ability to run them
- [ ] **Cloudflare**: DNS, tunnels and workers, edited rather than only read
- [ ] **Obsidian**: notes both ways, which is the closest thing to Dusk's own knowledge layer
- [ ] **GitHub Projects**: boards and cards as work, alongside the repositories core already reads
- [ ] **LubeLogger**: vehicle maintenance, where logging a service is the point
- [~] **Music Assistant, acting**: listed twice on purpose. Observing is built and shipped above; this row is the other half, wiring `Invoke` so playback actually starts
- [ ] **Calendar**: what is scheduled, and booking or moving it. The one plugin whose actions are mostly about a human's time rather than a system's state, so approval means something different here

---

## Next

1. Actions ([0015](../adr/0015-plugin-actions-and-events.md)). Every plugin shipped so far only observes, and the most recent one already wants more: Music Assistant declares its actions and implements `DryRun`, with `Invoke` deliberately absent. Until this lands, "a catalog that maintains itself" is half true, because nothing it knows can be acted on.
2. Reaching agents through `invoke` rather than new tools ([0041](../adr/0041-plugins-reach-agents-as-actions.md)), which is what stops an installed plugin inflating the tool list every session pays for.
3. The shared per-source API budget in the ingest scheduler. Each ingester is currently bounded only by its own interval, so two plugins hitting one API would each assume they had the whole quota.
4. Filtering drift, integrity and kind counts for a restricted viewer. They are the only reads a signed-in person sees unfiltered.

## Known gaps

- Catalog content is fed to agents with no trust boundary of its own. [ADR-0030](../adr/0030-account-allowlist.md) narrows *who* can reach that path but does nothing about a compromised repository inside an allowed account.
- Two repositories declaring the same entity is **reported but not resolved** ([0033](../adr/0033-graph-integrity.md)). `integrity` names both declarations; `Get` still returns whichever sorts first until a human picks.
- The MCP surface authenticates but does not authorize. A bearer token answers "may you read", not "what may you read", so any agent holding one reads the whole catalog ([0012](../adr/0012-viewing-auth.md)).
- Nothing keeps the chart in step with the application. A release adding a required value ships an image no published chart can deploy until someone notices ([0024](../adr/0024-charts-publishes-charts.md)).
- Access mode is fixed at registration. Changing it means editing the App's permissions on GitHub, which installations must then approve.
- Notes are ranked by pinned-then-id. [0031](../adr/0031-notes-are-files.md) wants kind to drive ranking so a gotcha outranks a todo without being pinned by hand.
- A note's refs and a relation's target are still unchecked at write time, deliberately, because the target may live in a repository Dusk cannot see. Both are now **reported** by `integrity` instead of failing silently ([0033](../adr/0033-graph-integrity.md)).
- Integrity reports every unresolvable ref, and in a partially adopted catalog most of them are legitimate. Nothing distinguishes "typo" from "not adopted yet".
- Drift matches a declaration to an observation by ref, or through an explicit `observed_as` when the two are named differently. Nothing infers the mapping, so an estate that uses its own names and has not written them shows those entities on both sides of the report.
- Drift stays silent about any kind nothing observes ([0038](../adr/0038-what-drift-may-say.md)). The cost is the reverse case: uninstalling the only plugin that observes a kind makes the report shrink rather than raise an alarm, so plugin health has to be watched somewhere else.
- Coverage is inferred from what a plugin returned rather than what it is responsible for, and a kind is global. One instance covering one cluster makes `service` watched everywhere, so a service declared in an unwatched cluster is still reported missing.
- Observations are stored under a reserved `ingester:` scope that occupies the repository slot. Anything treating a repository as clonable, or as something a sweep can prune, has to check `index.IsObserved` first. The sweep got this wrong once and deleted every observation on its first pass.
- A rate limit is recognised and logged but not waited out. A sweep that exhausts the budget gives up until the next one rather than resuming when the limit resets.
- The viewport matrix is run by hand and nothing repeats it. It has now been run against a real catalog and passes, but a regression is caught only by somebody thinking to look ([0025](../adr/0025-responsive-ui.md)).
- A browser session is a bearer token in a cookie by another name: it grants the whole catalog and cannot be revoked short of rotating `DUSK_MCP_TOKEN`, which signs it. Signing in with GitHub narrows a view; it does not close this path ([0036](../adr/0036-deriving-what-a-viewer-sees.md)).
- A restricted viewer's drift, integrity and kind counts are not filtered, so those blocks can count things the viewer cannot open.
- OAuth sign-in requests `repo` scope, which is far more than listing repository names needs. GitHub offers nothing narrower that still sees private repositories. A GitHub App ignores the scope entirely and grants what its installation permits, so this only bites a deployment pointed at a real OAuth App through the environment ([0036](../adr/0036-deriving-what-a-viewer-sees.md)).
- Identity sessions live in memory, so a restart signs everybody out. Every deploy is a restart, so this is routine rather than rare.
- A plugin's own output is kept in memory, bounded, and served per plugin, so anything its error string does not explain is readable without the pod. It dies with the process, so a plugin that crashed and was restarted has lost what it said as it went.
- **The PVC now holds something not rebuildable from git.** Installed plugin binaries live in the data directory, which is what lets a restart need no network, and weakens a guarantee that was previously absolute and simple to explain ([0042](../adr/0042-installing-plugins.md)).
- A plugin instance observing a source Dusk cannot reach locally needs that source's credentials mounted into Dusk's pod. Observing a second Kubernetes cluster therefore means a kubeconfig secret, which is the case [0039](../adr/0039-one-plugin-transport.md) left open by rejecting network plugins. The cheaper shape is an authenticated push endpoint, and that decision has not been made.
- Being signed in and being let in are two decisions in two places: `access.Policy` guards the surface, `access.OAuth` holds the identity, and the policy only consults the identity because it was handed one through `Recognize`. Sign-in shipped without that call and silently bounced everybody back to the login page, so a new credential has to be taught to the gate as well as minted.
- Anything a login or setup page references has to be routed outside the gate that page exists to open. The favicon was not, so it redirected to `/login` and rendered nothing. There is no rule enforcing this beyond the tests that now cover it.
- A grid or flex item's automatic minimum is its **min-content** width, so `1fr` alone lets one long ref widen a column past the screen. Every track that holds catalog content wants `minmax(0, 1fr)`. This shipped in the single-column mobile rule while the two-column desktop rule had it right, which is why it was invisible on a laptop and broke every phone.
- Verifying a layout needs a viewport that is actually applied. Chrome headless `--window-size` lays out wider than it captures, so its screenshots crop rather than reflow and read as overflow that is not there. Measure `document.body.scrollWidth` against `clientWidth` in the page, and take screenshots through a browser whose `innerWidth` you have confirmed.
- Every open pull request holds a partition of the index, rebuilt on every push to its branch. Nothing prunes an abandoned one until it closes ([0037](../adr/0037-pull-request-previews.md)).
- A preview's drift and integrity are unfiltered for a restricted viewer, so those counts can include entities they cannot open.
