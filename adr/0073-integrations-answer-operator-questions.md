# 73. Integrations answer operator questions, not a vendor checklist

Date: 2026-08-18

## Status

Accepted

## Context and Problem Statement

Dusk is an internal developer platform and shared memory for one homelab operator and their agents.
Its ecosystem should cover the systems homelabbers actually operate, but a list containing Home Assistant, Proxmox, TrueNAS, Synology, Unraid, Docker, Kubernetes, Flux, Cloudflare, Tailscale, Pi-hole, AdGuard, Grafana, Prometheus, GitHub, and backup tools can turn into a logo checklist.

That would produce thin integrations whose only achievement is that a product name appears on a page.
It would also duplicate shared protocols: Docker is still Docker when it runs on a NAS, and an OCI registry does not need a separate plugin for every vendor.
The opposite extreme, supporting only generic protocols, loses important semantics when a vendor API is the only place that knows about backups, storage pools, virtual machines, or effective policy.

The project needs a rule for deciding what belongs in the ecosystem and when a product needs its own integration.

## Considered Options

1. Build one plugin for every recognizable homelab product.
2. Support only shared protocols such as Docker, OCI, Kubernetes, and Prometheus.
3. Let third-party authors decide the ecosystem and keep the project neutral.
4. Prioritize operator questions, reuse a shared protocol where it preserves the answer, and use a vendor integration where it does not.

## Decision Outcome

Chosen: **prioritize integrations by the operator question they answer**.

Every integration must answer at least one inventory, knowledge, drift, or action question that belongs in Dusk.
It must define stable identity, complete-versus-partial observation, credential and upstream verification, and an explicit action boundary.
Support means an installable release plus a fake-upstream contract test and a representative real, read-only end-to-end check.

A shared protocol is preferred when it preserves the answer and the upstream identities.
This is why one container-host plugin can operate Docker on a Linux server, NAS, or hypervisor, and why one OCI plugin covers several registries.
A product-specific plugin is the right answer when the shared layer cannot see the fact that matters, such as a NAS backup result or a Proxmox guest.

The ecosystem page distinguishes released support, partial coverage through a shared protocol, and future priorities.
It never promotes a planned repository or a product name to supported status.

## Consequences

### Good

- Each plugin has a reason to exist beyond marketplace size.
- Shared protocols avoid nearly identical clients that drift apart.
- Vendor plugins remain available where generic APIs throw away the useful semantics.
- “Supported” has a testable meaning tied to a release and real system.
- The roadmap stays aligned with a single homelab operator instead of enterprise feature parity.

### Bad

- The supported logo count grows more slowly than the code could make it grow.
- A product may be only partly covered, such as Docker workloads on a NAS without storage or backup health.
- Representative real-system checks require access to hardware and services the project does not own.
- Deciding whether a shared protocol preserves enough meaning requires judgement and may be revisited as APIs change.

### Rejected because

- **One plugin per product** was rejected because it rewards surface area, duplicates protocols, and makes shallow support look finished.
- **Shared protocols only** was rejected because the operational facts a homelabber needs often live above them.
- **Leaving the ecosystem entirely to third parties** was rejected because the project still needs a coherent supported path and examples that prove its own plugin contract.

