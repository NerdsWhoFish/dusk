# 40. GitHub is core, Kubernetes is the first plugin

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

[ADR-0034](0034-ingesters-in-tree-first.md) put the Kubernetes ingester in the main binary on purpose, and said plainly that this was sequencing rather than a destination: the machinery is the hard half, so build it where a test can reach it, then let the plugin host become a second implementation of a proven interface.

It also recorded the price, in its own Bad consequences: "The Kubernetes client is now a dependency of the main binary, and it is a heavy one. Every operator carries it whether or not they run a cluster."

[ADR-0039](0039-one-plugin-transport.md) has now settled the transport, so the host can be built.
That makes the deferred question live: which integrations move out, and which never do.

Answering it one integration at a time produces a boundary nobody can predict, and "is this a plugin?" gets re-argued from scratch every time. The boundary needs a rule.

## Considered Options

1. **Everything is a plugin**, including GitHub. Core is the graph, the index and the UI.
2. **Nothing moves.** Integrations stay in tree and the plugin protocol serves third parties only.
3. **Core owns what Dusk cannot function without.** Everything Dusk merely observes is a plugin.

## Decision Outcome

Chosen: **option 3**, which decides both cases in front of us.

**GitHub is core, permanently.**
**Kubernetes becomes a plugin, and is the first one.**

### GitHub is not an integration, it is the substrate

The rule turns on a question with an unambiguous answer: can Dusk do its job without this?

[ADR-0001](0001-git-as-source-of-truth.md) makes git the source of truth, and GitHub is how Dusk reads it.
[ADR-0005](0005-github-app-and-access-modes.md) gives Dusk a GitHub App and its credentials, [ADR-0006](0006-reconcile-triggering.md) has GitHub deliver the webhooks that trigger reconciles, [ADR-0029](0029-reading-repositories.md) and [ADR-0032](0032-tarball-reads.md) are how a repository is read at a commit, [ADR-0010](0010-mcp-surface.md) commits writes back through it, and [ADR-0037](0037-pull-request-previews.md) renders unmerged refs from it.

A Dusk with the GitHub integration removed does not have fewer sources. It has no catalog.

The practical argument is as strong as the architectural one.
Dusk already authenticates to GitHub.
A GitHub plugin would have to be issued its own GitHub credentials, duplicating an App that already exists, in order to describe the system Dusk is already talking to, and it would need a second credential to talk back to Dusk. Two new secrets to replace none.

### Kubernetes is exactly what a plugin is for

Nothing in Dusk depends on Kubernetes. It is a system some operators run and want described, which is the definition of an observed system.

It is the right first plugin for a reason beyond being available: **it already works in tree**, so the protocol is measured against behaviour that exists rather than behaviour imagined for it.
If `PluginService` cannot express what `internal/ingest/kubernetes.go` does today, including the namespace and plumbing filters and [ADR-0011](0011-ingester-scheduling.md)'s rule that a failed run never deletes, then the protocol is wrong and this is where that is discovered.

A toy first plugin would have proven nothing.

### The in-tree ingester is removed only once the plugin replaces it

Moving Kubernetes out is a removal from a running product, so the order is fixed:

1. The plugin host lands in core.
2. The plugin is built in its own repository against the SDK, and observes a real cluster.
3. Only then does `internal/ingest/kubernetes.go` go, and `k8s.io/client-go` with it.

Deleting first would regress a working feature into a partially built one, which is the failure [ADR-0011](0011-ingester-scheduling.md) exists to prevent in the small and is no more acceptable in the large.

## Consequences

### Good

- `k8s.io/client-go` leaves the binary. Every operator stops carrying a Kubernetes client to run a catalog, which is ADR-0034's stated cost finally paid.
- The plugin protocol is proven against a real integration with real edge cases, before third parties depend on it.
- "Is this core?" has an answer that does not need a meeting: remove it and see whether Dusk still has a catalog.
- The three in-house ingesters that [ADR-0002](0002-plugin-protocol.md) requires before the contract stabilises now have somewhere to live.

### Bad

- **Two repositories now have to move in step.** A protocol change is a coordinated release, and a version skew between host and plugin is a new failure mode that in-tree code could not have.
- Observing a cluster stops being free. An operator installs and configures something where previously they set one environment variable, and that friction lands on the integration most likely to be somebody's first.
- The rule is clear at the edges and not in the middle. Flux is arguably core to how Dusk is deployed and clearly not core to what Dusk is, and the next integration like it will be argued again.
- Kubernetes keeps working in tree until step 3, so for a while there are two ways to observe a cluster and one of them is the wrong one.

### Rejected because

- **Option 1** was rejected because a GitHub plugin would need duplicate credentials to describe the system Dusk is already authenticated against, and because git being the source of truth makes GitHub substrate rather than a source among sources.
- **Option 2** was rejected because it contradicts [ADR-0034](0034-ingesters-in-tree-first.md)'s own stated destination, leaves a heavy dependency in every binary forever, and would let the plugin protocol be published without ever being proven by something real.
