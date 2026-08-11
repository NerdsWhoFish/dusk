# 2. Plugins are subprocesses with a published contract, not a linked API

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk needs to pull entities from many systems: Kubernetes, Flux, GitHub, Home Assistant, and whatever a given user runs.
Hard-coding those integrations does not scale, and the integrations are exactly the part the community is best positioned to write.

The stated requirement is that **plugin authors can use whatever tooling they want**.
That is a language-agnostic requirement, not merely an extensibility one, and it rules out anything that assumes the plugin is written in the host's language.

Timing matters here.
Extension points built before there are real consumers tend to be the wrong ones, and a plugin API is close to impossible to change once published.
The counterweight is that a **process boundary** is genuinely expensive to retrofit, unlike an in-process API.
So the boundary is decided now, while the surface is kept as small as possible.

## Considered Options

1. **In-process Go plugin API**, compiled or via Go's `plugin` package.
2. **`hashicorp/go-plugin`**, the pattern proven by Terraform, Vault, Nomad, and Packer.
3. **WASM**, via wasmtime or Extism.
4. **Subprocess with stdout JSON**, in the style of Prometheus textfile collectors.
5. **Subprocess with gRPC over a unix socket**, with a published, language-neutral handshake.

## Decision Outcome

Chosen: **one protobuf schema, two transports**, combining options 4 and 5.

The `.proto` is the single source of truth for entity types, versioning, and validation.
There is one contract, expressed two ways:

- **Tier 1, ingesters.** The host execs a binary, the binary writes **protojson** (protobuf's canonical JSON mapping) on stdout, the host ingests it. Stateless and batch. Ships first.
- **Tier 2, interactive plugins.** The identical messages over gRPC on a unix socket provided by the host, which owns process lifecycle. Added when a plugin genuinely needs to be long-lived or bidirectional.

Because protojson is ordinary JSON, a Tier 1 plugin can still be a shell script and is still testable with `./my-plugin | jq`, while validating against the same schema a Tier 2 plugin compiles against.
A plugin can graduate from Tier 1 to Tier 2 without changing its data model.

The `.proto` is a published artifact covered by the project license.

Three real ingesters (Kubernetes, Flux, GitHub) will be written in-house before the contract is declared stable, so that the protocol is derived from actual use rather than imagined use.

### Consequences

#### Good

- Tier 1 is writable in any language, including a shell script, and is testable with `./my-plugin | jq`. That is what makes the language-agnostic promise true rather than nominal.
- A crashing plugin cannot take down the host, and a malicious one is bounded by process permissions.
- No ABI to version, and no toolchain coupling between host and plugin.
- Most plugins will never need Tier 2, so the common case carries almost no protocol burden.
- Because git is the source of truth, an integration can also skip the protocol entirely and just write files, which is the zero-friction floor.

#### Bad

- Subprocess exec has real per-invocation overhead, which matters if ingesters are run frequently.
- Two transports still means two code paths to implement and test, even though they share one schema.
- Tier 2 requires the host to manage process lifecycle, health, and cleanup, which is meaningful work that Tier 1 avoids.
- Publishing a `.proto` early creates a compatibility obligation that arrives before the design is fully proven.

#### Rejected because

- Option 1 fails the language requirement outright, and Go's `plugin` package additionally requires exact toolchain matching, which is unusable in practice.
- Option 2 is architecturally right but practically wrong here. Its handshake and broker are Go-host-centric, and implementing a non-Go plugin against it is painful enough that the language-agnostic promise would be technically true and functionally false. Its architecture is adopted; its implementation is not.
- Option 3 is a poor fit for the workload. Ingesters exist to call network APIs and read credentials, which is exactly what WASM sandboxes make difficult, so the sandbox benefit is paid for with the capability the plugins need most.
