# Dusk

**A service catalog that maintains itself.**

Dusk is a catalog and knowledge layer for your systems.
Humans browse it, agents read and write it over MCP, and git is the source of truth for both.

Every knowledge tool dies the same way: the curation burden falls on a human who has better things to do.
Dusk assumes the agents doing the work can also do the documenting, so the catalog updates as a byproduct of the work rather than as a chore after it.

> Status: **pre-implementation.** The design is being written before the code. See [DESIGN.md](DESIGN.md).

## What makes it different

- **You never fork it.** Run the binary, point it at a repo, it reconciles. No Node monorepo to own, no upgrade merge conflicts.
- **Git is the source of truth.** Entities are markdown with frontmatter in the repos they describe. Agents read files directly, and agent writes are file edits, so review and history come for free.
- **Pull requests are first class.** Any open PR renders as the catalog as it would be after merge, with a semantic diff of what actually changed.
- **Plugins are subprocesses.** A plugin can be a shell script that prints JSON. Write one in any language.

## Documentation

- [DESIGN.md](DESIGN.md) covers the architecture, the decisions, and the open questions.
- [adr/](adr/) holds the decision records, including the alternatives that were rejected and why.
- [docs/dusk-md.md](docs/dusk-md.md) is the reference for the `dusk.md` file a repository uses to join the catalog.
- [docs/reconcile.md](docs/reconcile.md) covers turning a repository into the graph, and `dusk validate` for checking a checkout locally.
- [docs/mcp.md](docs/mcp.md) is the agent-facing surface: the tools, how to connect, and what is not built yet.
- [docs/controller.md](docs/controller.md) covers what keeps the catalog current: discovery, the account allowlist, webhooks, the poll floor, and the API budget.
- [docs/storage.md](docs/storage.md) covers the materialized graph: how it is keyed, what it stores, and how search works.
- [docs/packages.md](docs/packages.md) maps every package to its job, and the rules for adding one. Read it before writing anything new.
- [docs/philosophy.md](docs/philosophy.md) is the posture behind the design.
- [docs/status.md](docs/status.md) tracks what is built and what is not.

## License

Apache 2.0. Contributions are accepted under the DCO. See [ADR-0003](adr/0003-license.md).
