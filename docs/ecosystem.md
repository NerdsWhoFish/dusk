# The homelab ecosystem

Dusk integrations earn their place by answering a question an operator or agent actually has.
A logo, an API client, or a count of imported objects is not enough.

## Supported now

| Area | Integration | What it answers |
| --- | --- | --- |
| Source and history | GitHub, built into Dusk | What did we declare, who changed it, and what would this pull request change in the catalog? |
| Clusters | [Kubernetes](https://github.com/NerdsWhoFish/dusk-plugin-kubernetes) | What is running, where is it running, and which workload can be restarted or scaled for this service? |
| GitOps | [Flux](https://github.com/NerdsWhoFish/dusk-plugin-flux) | What does GitOps believe is deployed, is reconciliation healthy, and which Kubernetes objects came from this release? |
| Container hosts | [Docker Engine](https://github.com/NerdsWhoFish/dusk-plugin-container-host) | What runs outside Kubernetes, which stack owns it, is it healthy, and can it be started, stopped, or restarted? |
| Images | [OCI registries](https://github.com/NerdsWhoFish/dusk-plugin-oci) | Which repositories exist across Harbor, GHCR, Docker Hub, GitLab, ECR, Artifactory, Zot, or a plain registry, and which tags are operationally useful? |
| Network | [Firewalla](https://github.com/NerdsWhoFish/dusk-plugin-firewalla) | Which devices and networks exist, where is a device connected, and when was it last active? |
| Home automation | [Home Assistant](https://github.com/NerdsWhoFish/dusk-plugin-home-assistant) | What entities, sensors, automations, scripts, and controls exist right now, and which low-risk actions may an operator enable? |
| Media | [Music Assistant](https://github.com/NerdsWhoFish/dusk-plugin-music-assistant) | Which players and groups exist, what are they doing, and can playback or power be controlled? |
| Personal knowledge | [Obsidian](https://github.com/NerdsWhoFish/dusk-plugin-obsidian) | Which curated boards, cards, customers, and notes belong in the operational graph? |

The container-host plugin speaks the Docker Engine API over a local socket, TCP, or SSH.
That makes it useful on a Synology NAS, Unraid server, or Proxmox host when Docker is the thing being operated.
It does **not** claim to inventory storage pools, virtual machines, backups, hypervisor health, or vendor-specific services on those systems.

## What comes next

The next integrations are ordered by missing operator questions, not popularity:

1. **Backup and storage health** for Synology, TrueNAS, Unraid, and common backup tools: what is protected, when did it last succeed, and what is silently outside the backup set?
2. **Virtualization** for Proxmox: which guests exist, where do they run, what depends on them, and which lifecycle actions are safe to expose?
3. **DNS and edge** for Cloudflare, Tailscale, Pi-hole, and AdGuard: which name, route, or policy makes a service reachable, and where do declared and effective configuration differ?
4. **Metrics** for Prometheus and Grafana: which alert or dashboard explains an entity's health without copying a time-series database into the catalog?

One integration may cover several products when they expose the same honest boundary.
OCI registries and Docker hosts already work that way.
A vendor-specific plugin is warranted when the shared protocol cannot answer the important question or preserve the vendor's identity and safety semantics.

## The bar for another integration

Before adding one, write down:

- The inventory, knowledge, drift, or action question it answers
- The stable identity that survives addresses, restarts, and display-name changes
- Whether an observation is complete, partial, or failed
- Which actions are safe enough to offer and which are deliberately absent
- How credentials are scoped and how the upstream identity is verified
- The fake-upstream contract test and the real, read-only end-to-end check

The integration is supported when its installable release exists and those checks pass.
An unreleased repository or a roadmap row is not support.

## Not on the roadmap

Dusk is for one trusted homelab operator and their agents.
Multi-user administration, RBAC, SSO, organization charts, compliance workflows, scorecards, scaffolding factories, proposal queues, and Backstage parity are outside the product boundary.

