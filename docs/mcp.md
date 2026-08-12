# MCP

The MCP surface is how agents read the catalog.
It is the product's main interface rather than an integration, because agents are the primary consumer.

Settled in [ADR-0010](../adr/0010-mcp-surface.md) and [ADR-0014](../adr/0014-agent-context-injection.md), implemented in `internal/mcp`.

## Connecting

Dusk serves streamable HTTP at `/mcp` on the private host.

```json
{
  "mcpServers": {
    "dusk": {
      "type": "http",
      "url": "https://dusk.example.com/mcp",
      "headers": { "Authorization": "Bearer YOUR_TOKEN" }
    }
  }
}
```

## Who may read the catalog

**The surface is off until something says.** One read returns the whole catalog, so Dusk will not pick a default:

| Setting | Effect |
| --- | --- |
| `DUSK_MCP_TOKEN` | Require that bearer token. Compared in constant time |
| `DUSK_TRUSTED_NETWORK=true` | Serve it unauthenticated |
| Neither | `/mcp` answers 503 explaining how to turn it on |

Setting both is an error rather than the stricter of the two, because two answers to "who may read this" is an unanswered question.

Whichever applies is written to the boot log, and the unauthenticated mode warns on every start.
[ADR-0012](../adr/0012-viewing-auth.md) permits a trusted-network mode for LAN and single-operator deployments and requires it to be explicit, which is why it cannot be arrived at by accident.

## Four tools, each fat

The tool list is spent on every session before any work happens, so its size is a product constraint.
One tool per schema operation would produce thirty tools and cost a dozen calls to answer one question.

| Tool | What it answers |
| --- | --- |
| `search(query, kind?, limit?)` | "Where is the thing called X" |
| `get(ref)` | Everything about one entity, including its connections |
| `neighbors(ref, depth?)` | "What breaks if this goes away" |
| `changes()` | What Dusk last read from git, per repository |

`get` is deliberately fat.
An agent asking about an entity wants the whole picture, so it gets the description, attributes, relations and provenance in one call rather than five.

## Three rules the answers follow

**Every answer carries refs that feed back into `get`.**
Composability over completeness: a search result is not a dead end, it is the input to the next call.

**Answers are markdown, not nested JSON.**
Agents reason better over prose than over deeply nested objects.

**Absence is explained, never silent.**
Searching for something nobody has declared says so, and points at `changes`.
An agent that cannot tell "not in the catalog" from "the catalog is empty" will invent the difference.

## What `changes` is for

Reconcile status is the difference between a stale answer and a missing one.

It reports, per repository, the commit last read and how many entities came from it, and separates three states that look alike from the outside: repositories that declared entities, repositories that failed, and repositories with no `dusk.md` at all.

The last of those is the common case and is not a failure.

## Scope

Queries run against the **default view**: every repository at its own default branch.

Repositories disagree about what that branch is called, so there is no single git ref meaning "the catalog as it stands", and the index keeps a record of each repository's default instead.
See [storage](storage.md).

Pull request previews render a specific ref and are a UI concern, so the MCP tools do not take one.

## Not built yet

**Writes.** `declare`, `note`, `relate`, `mintKind` and `push` all wait on proof tokens ([ADR-0009](../adr/0009-proof-tokens.md)) and the commit queue. Today the surface is read-only, which [ADR-0005](../adr/0005-github-app-and-access-modes.md) treats as a supported posture rather than a degraded one.

**Notes.** [ADR-0010](../adr/0010-mcp-surface.md) has `search` covering entities *and* notes together, because "how do I reach the zwave pi" is a note while "the zwave pi" is an entity. Notes have no home in `dusk.md` yet ([ADR-0026](../adr/0026-dusk-md-schema.md)), so search covers entities alone.

**Authorization derived from repository access.** A bearer token answers "may you read", not "what may you read": it grants the whole catalog. [ADR-0012](../adr/0012-viewing-auth.md) decides that authorization should be *derived* from what GitHub says a viewer can see, which for an agent means presenting a GitHub token and having Dusk filter to the repositories it can read. The index is already partitioned by repository, so that filter is a predicate rather than a permission model. It earns its keep when a second person exists, and not before.
