# 20. Plugins contribute UI as Web Components, in three tiers

Date: 2026-08-11

## Status

Accepted, deferred to after the core ships. Amended, see [Amendments](#amendments).

## Context and Problem Statement

A plugin knows things Dusk does not.
A Kubernetes plugin could show pod status and let someone tail logs; a Flux plugin could show reconciliation state.
Rendering that as a generic key-value dump wastes what the plugin knows.

So plugins should be able to contribute UI.
The question is by what mechanism, and it is load-bearing, because one obvious answer destroys the product.

**Compiling plugin UI into the application at build time is what makes Backstage require a fork.** Its plugins are npm packages compiled into your app, which is precisely why you clone `create-app` and own a Node monorepo forever. Everything in [ADR-0005](0005-github-app-and-access-modes.md) and the GitOps posture exists to avoid that, and plugin UI is where the pressure returns disguised as a feature.

Runtime loading does not have that problem.
The binary stays a binary and nobody forks anything.
But "the plugin ships a React component" has a specific technical trap: two React instances on one page breaks hooks, as a hard error.
A plugin cannot bundle its own React, so it must import the host's, which makes **Dusk's React version a permanent public contract** that can never be upgraded without breaking every plugin.

## Considered Options

1. **Build-time npm plugins**, the Backstage model.
2. **Module Federation** or equivalent remote React components.
3. **Web Components** shipped by the plugin binary and loaded at runtime.
4. **A declarative view spec**, where the plugin describes what to render and Dusk renders it.
5. **iframes**, fully isolated.

## Decision Outcome

Chosen: **three tiers, 4 then 3 then 5**, matching the shape the plugin protocol already has in [ADR-0002](0002-plugin-protocol.md).

| Tier | Mechanism | Trust required |
| --- | --- | --- |
| 1 | **Declarative view spec.** The plugin declares "these fields as a table", "this as a status badge". Dusk's own React renders it. No JavaScript. | None. The default. |
| 2 | **Web Component.** The plugin ships a custom element for real interactivity. | Opt-in. Runs in Dusk's origin. |
| 3 | **iframe.** Separate origin, fully isolated. | For heavyweight or untrusted views. |

Most plugins want to present their data well rather than paint pixels, so the zero-trust tier is the one that must be easy, and shipping JavaScript is a deliberate step.

### Why Web Components rather than React

The plugin ships a custom element, not a React component.
Inside it, the author uses React, Preact, Svelte, or nothing, bundled independently.

Dusk's React renders `<dusk-k8s-pods entity-ref="service:home/scrypted" />` like any other element.
No shared runtime, no version coupling, and React can be upgraded freely.

This is also the same promise [ADR-0002](0002-plugin-protocol.md) makes on the data side: plugin authors use their own tooling.

### How the asset reaches the browser

`Describe` gains UI contributions: the element tag and where it mounts, meaning which entity kinds and which page slots.

The binary embeds its built JavaScript.
Dusk pulls it over the existing gRPC connection and serves it **from its own origin**, content addressed at `/plugin-assets/<plugin>/<sha>.js`.

There is no CDN, no external fetch at render time, and no network dependency introduced into the page.

### Tier 2 is opt-in, default deny

**Shadow DOM is style isolation, not a security boundary.**
Plugin JavaScript in Dusk's origin can read the session, call the API as the user, and inject anywhere on the page.
That is equally true of Web Components and of federated React, and it must not be glossed.

So Tier 2 follows the same posture as actions in [ADR-0015](0015-plugin-actions-and-events.md): denied by default, enabled per plugin by a deliberate act, with the consent prompt stating plainly that this plugin's UI runs with access to the session.

## Consequences

### Good

- The binary stays a binary. Nobody forks Dusk to get plugin UI, which is the single property this decision exists to protect.
- React can be upgraded at will, because no plugin depends on its version.
- Plugin authors keep framework freedom, consistent with the data side of the contract.
- The zero-trust tier covers the common case, so most plugins never ship JavaScript and never need a trust decision.
- Assets served from Dusk's own origin means no CDN dependency and no third-party host in the request path.
- Content addressing makes caching trivial and swaps atomic.

### Bad

- Three tiers is three rendering paths to build, document, and support, and authors will pick the wrong one.
- A plugin wanting a bespoke visualisation gets an iframe, with all the sizing, theming, and navigation friction that implies. They will be annoyed, and that is the price of the binary staying a binary.
- Tier 2 is a genuine trust decision users are not well equipped to make, and "this plugin's UI can read your session" is a hard thing to present usefully.
- Web Components bundle their own framework, so payloads are larger than federated components would be.
- Theming across the Shadow DOM boundary requires passing CSS custom properties through deliberately, which is extra work for every author.
- The declarative view spec is a vocabulary, and vocabularies grow. It will be under constant pressure to become a layout language.

### Rejected because

- Build-time npm plugins were rejected outright. That is the Backstage fork, and avoiding it is a founding constraint rather than a preference.
- Module Federation was rejected because it makes the host's React version a contract every plugin is coupled to, permanently. The upgrade path it forecloses is worth more than the ergonomics it buys.

## Amendments

Amendment policy: [ADR-0028](0028-amending-adrs.md).

### 2026-08-16: tier 1 mounts only where a result set comes from

The tier table said what each tier is and what it costs in trust, and said nothing about where each can mount.
A contribution also names a slot, and the plugin's own page supplies no entity to any view mounted there, so tier 1 in that slot rendered its own `empty` text for ever.

Tier 1 draws a result set, so it mounts on an entity page, which supplies the entity, or in a page's `view` block, which supplies its `ref` or `query` ([ADR-0035](0035-blocks-resolve-server-side.md)).
On the plugin's own page it is refused, in the plugin's own conformance tests and again by the host, and only tiers 2 and 3 are available there.

The decision itself is unchanged, including the claim that most plugins never ship JavaScript and never need a trust decision.
[ADR-0064](0064-a-declared-view-mounts-where-a-result-set-comes-from.md) records the reasoning and the alternative, which was to have the plugin page resolve a set of its own.

### 2026-08-16: the account allowlist is the trust boundary, and tier 2 has no second gate

**"Tier 2 is opt-in, default deny" above was never implemented, and it is now withdrawn rather than owed.**
An audit found that installing a plugin mounts its element and serves its JavaScript with no enabling step: `Manager.Views`, `Contributions` and `Asset` ask nothing, the entity endpoint serves what they return, and the install card speaks about the subprocess and never about the session.
Actions kept their gate, so the two halves of "the same posture as actions" parted company without anybody deciding they should.

The operator has decided they should.
[ADR-0030](0030-account-allowlist.md) and [ADR-0042](0042-installing-plugins.md) put the boundary at the account: only an allowlisted account is reconciled at all, and a plugin is installed from a release in an allowlisted organisation with its checksum verified.
Everything past that line is code the operator already chose to run, and a plugin that ships a subprocess with the host's token is not made safer by a second prompt about its JavaScript.
The design target is one operator and their agents ([ADR-0027](0027-design-target.md)), not a marketplace of strangers, and a consent dialogue that always says yes teaches an operator to click through the one that should have said no.

What is given up is real and is the cost of the decision.
A plugin's UI runs in Dusk's origin with the session, so an installed plugin can read anything the signed-in viewer can, and there is no per-plugin switch to withdraw that short of uninstalling it.
The blast radius of a compromised release in an allowlisted organisation is therefore the whole browser session, not just the subprocess.

Tier 3, the sandboxed iframe this record specified for untrusted UI, was also never built, and nothing needs it while the boundary sits where it now sits.
It stays in the record as the answer if that ever changes.
