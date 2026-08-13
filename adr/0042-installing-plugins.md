# 42. Plugins are found by org, installed from a release, and cached on disk

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

[ADR-0039](0039-one-plugin-transport.md) settled how Dusk talks to a plugin and [ADR-0040](0040-core-and-plugins.md) settled what should be one.
Neither says how a plugin gets onto the machine.

Left unanswered, the answer becomes "the operator downloads a binary and puts it somewhere", which is the friction the plugin idea exists to remove: an integration nobody can install is not extensible, it is just modular.

Four things have to be decided together, because deciding them apart produces a design that cannot update itself:

- **Discovery.** How an operator learns a plugin exists.
- **Installation.** How the binary arrives.
- **Update.** How a new version arrives, and who says yes.
- **Persistence.** Where it lives between restarts, given Dusk in Kubernetes gets a fresh container every rollout.

The last one is not a detail. A plugin re-downloaded on every boot makes GitHub a hard runtime dependency for a service that is otherwise happy offline, and turns a rollout into a race against a rate limit.

There is also a trust problem that this ADR creates and must therefore answer.
[ADR-0030](0030-account-allowlist.md) narrowed which accounts Dusk will *read* repositories from, on the grounds that catalog content reaches agents. This decision has Dusk **download and execute** code, which is a strictly larger claim on an operator's trust than reading a markdown file.

## Considered Options

1. **Manual installation.** The operator places a binary and configures a path.
2. **A curated registry**, a list Dusk publishes and updates, listing approved plugins.
3. **A package manager's ecosystem**, publishing plugins to npm, OCI, or Homebrew and installing from there.
4. **GitHub orgs as the unit of discovery**, with a naming convention and GitHub releases as the artifact.

## Decision Outcome

Chosen: **option 4**.

A **marketplace is a list of GitHub orgs.** `FetchHQ` is the default and is not special beyond being first in the list. Within an org, any repository named `dusk-plugin-<name>` is a plugin. Dusk lists them in its UI, installs from the GitHub release, keeps the binary on disk, and offers updates the operator approves.

### The naming convention is the registry

There is no list to maintain, no submission process and nobody to be a gatekeeper.
Publishing a plugin means naming a repository `dusk-plugin-something` in an org somebody has added, and cutting a release.

Option 2 was the alternative and it fails on the thing it appears to provide. A curated list implies review, review implies a reviewer, and a project that cannot promise one is publishing an assurance it does not have. Worse, curation would sit exactly where an operator stops looking, because a listed plugin reads as an endorsed one.

Option 3 was rejected because every one of those ecosystems is a second account, a second set of credentials and a second publishing story for the plugin author, in exchange for distribution Dusk does not need. The plugin already lives in a GitHub repository, and its releases are already there.

### The org list is the trust boundary, and it is the whole of it

Adding an org is the security decision. It says: I will run code these people publish.

That is deliberately a coarse, human-sized decision rather than a per-plugin one, because per-plugin approval trains an operator to click through prompts, and the tenth prompt gets the same attention as an EULA. One decision, made once, understood at the time it is made.

Dusk states the consequence plainly rather than dressing it up: **a plugin runs as a subprocess of Dusk with Dusk's own permissions.** [ADR-0002](0002-plugin-protocol.md) bounds a plugin by process permissions, which is real isolation from *Dusk's memory* and no isolation at all from Dusk's filesystem, network or Kubernetes ServiceAccount.

Three things follow, and none is optional:

- **Checksums are verified against the release's checksum file** before anything is executed. GoReleaser produces one, which is why the plugin release convention requires GoReleaser rather than merely suggesting it.
- **The version installed is recorded**, so what is running is answerable without inspecting a binary.
- **An update is never silent.** Dusk shows that one is available and installs it when a human says so. A plugin that could update itself under an operator could change what it does after the decision to trust it was made.

### The disk is the source of truth at boot, not GitHub

An installed plugin is written to Dusk's data directory, which in Kubernetes is the PVC [ADR-0008](0008-storage.md) already requires for the index.

Dusk starts from what is on disk, and reaches GitHub only to check for updates or to install something new.
A restart of a fully offline Dusk starts every plugin it had.

This means the PVC now holds something that is **not** disposable. The index is rebuildable from git by contract; a plugin binary is not, and its `helm.sh/resource-policy: keep` matters for a second reason now.

### Updates are offered against a channel the operator can see

Dusk polls the orgs on the same schedule and with the same budget discipline the reconcile sweep uses ([ADR-0006](0006-reconcile-triggering.md)), because listing releases across several orgs is exactly the kind of ungated periodic API traffic [ADR-0017](0017-engineering-policy.md) warns about. A marketplace nobody opened should cost almost nothing.

## Consequences

### Good

- Publishing a plugin is naming a repository and cutting a release. No registry, no submission, no gatekeeper, and no second ecosystem to learn.
- An operator installs from a UI rather than placing binaries, which is what makes the extensibility real rather than nominal.
- Dusk boots offline with everything it had, and a rollout does not depend on GitHub being up.
- The trust decision is coarse, explicit and made once, rather than a stream of prompts that train people to approve without reading.
- Anybody can run their own marketplace by adding their own org, including a private one, without asking.

### Bad

- **A plugin runs with Dusk's permissions, including its Kubernetes ServiceAccount.** Adding an org is a larger grant than it sounds like, and no amount of wording in a dialog makes that safe. Real isolation would need a sandbox, and [ADR-0002](0002-plugin-protocol.md) already rejected WASM for cutting off the capabilities ingesters exist to use. There is no good answer here, only a stated one.
- Checksum verification proves the binary matches its release. It proves nothing about whether the release should be trusted, and a compromised publisher passes every check.
- The naming convention is a global namespace with no owner. Two orgs can both publish `dusk-plugin-github`, and the operator's org ordering silently decides which one wins.
- The PVC now holds something not rebuildable from git, which weakens a guarantee that was previously absolute and simple to explain.
- Polling several orgs for releases costs API budget for a feature most operators use rarely.
- Dusk now has an updater, which is a category of software with a long history of being the thing that breaks.

### Rejected because

- **Option 1** was rejected because an integration that requires placing a binary by hand will not be installed, which makes the plugin protocol an architecture rather than a feature.
- **Option 2** was rejected because curation implies a review nobody has committed to perform, and a curated list reads as an endorsement precisely where an operator stops evaluating.
- **Option 3** was rejected because it costs the plugin author a second account, second credentials and a second release process to reach distribution the author already has on GitHub.
