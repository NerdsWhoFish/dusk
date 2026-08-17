# Dusk

**A service catalog that maintains itself.**

Dusk is a catalog and knowledge layer for your systems.
Humans browse it, agents read and write it over MCP, and git is the source of truth for both.

Every knowledge tool dies the same way: the curation burden falls on a human who has better things to do.
Dusk assumes the agents doing the work can also do the documenting, so the catalog updates as a byproduct of the work rather than as a chore after it.

> Status: **1.0.0.** Running in production against a real estate, with plugins observing Kubernetes, Flux, container hosts, OCI registries and a router. [DESIGN.md](DESIGN.md) is the architecture, [docs/status.md](docs/status.md) is what is built and what is not.

![The Dusk homepage: a search box, a count of every kind in the catalog, what has drifted, what Dusk last read, and the notes agents have written](docs/images/home.png)

The homepage is declared, not configured: `.dusk/home.md` in a config repository, where every block is a query ([docs/pages.md](docs/pages.md)).

![An entity page for a checkout service: its ref, its description, a Gotchas section, and the notes attached to it rendered in full](docs/images/entity.png)

An entity is one markdown file in the repository that owns it, and the notes agents attach to it are rendered where somebody will be standing when they need them.

## What makes it different

- **You never fork it.** Run the binary, point it at a repo, it reconciles. No Node monorepo to own, no upgrade merge conflicts.
- **Git is the source of truth.** Entities are markdown with frontmatter in the repos they describe. Agents read files directly, and agent writes are file edits, so review and history come for free.
- **Pull requests are first class.** Any open PR renders as the catalog as it would be after merge, with a semantic diff of what actually changed.
- **Plugins are subprocesses.** A plugin can be a shell script that prints JSON. Write one in any language.

![The plugins page: each plugin with its version and an install button, above the sentence that a plugin runs as a subprocess with Dusk's permissions](docs/images/plugins.png)

A plugin is installed from a release in an allowlisted organisation, with its checksum verified before anything runs. That account list is the trust boundary, which is why the page says what installing one means rather than asking a question nobody reads ([ADR-0020](adr/0020-plugin-ui.md), [ADR-0042](adr/0042-installing-plugins.md)).

## Documentation

- [DESIGN.md](DESIGN.md) covers the architecture, the decisions, and the open questions.
- [adr/](adr/) holds the decision records, including the alternatives that were rejected and why.
- [docs/dusk-md.md](docs/dusk-md.md) is the reference for the `dusk.md` file a repository uses to join the catalog.
- [docs/reconcile.md](docs/reconcile.md) covers turning a repository into the graph, and `dusk validate` for checking a checkout locally.
- [docs/mcp.md](docs/mcp.md) is the agent-facing surface: the tools, how to connect, and what is not built yet.
- [docs/controller.md](docs/controller.md) covers what keeps the catalog current: discovery, the account allowlist, webhooks, the poll floor, and the API budget.
- [docs/storage.md](docs/storage.md) covers the materialized graph: how it is keyed, what it stores, and how search works.
- [docs/ingest.md](docs/ingest.md) covers the half of the catalog nobody types: what an ingester promises, why a failure never deletes, and why every one of them is a plugin.
- [docs/kinds.md](docs/kinds.md) is the vocabulary: how kinds are counted rather than configured, what minting one changes, and why a near match warns instead of refusing.
- [docs/pages.md](docs/pages.md) is the homepage: the `.dusk/home.md` a config repository declares, every block type, and the query grammar behind them.
- [docs/plugins.md](docs/plugins.md) is for plugin authors: what an action's parameter schema may contain, which shape becomes which control, and what a form refuses before the plugin sees it.
- [docs/packages.md](docs/packages.md) maps every package to its job, and the rules for adding one. Read it before writing anything new.
- [docs/philosophy.md](docs/philosophy.md) is the posture behind the design.
- [docs/status.md](docs/status.md) tracks what is built and what is not.

## License

Apache 2.0. Contributions are accepted under the DCO. See [ADR-0003](adr/0003-license.md).
