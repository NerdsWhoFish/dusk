# 71. MCP results are dual-purpose and bounded

Date: 2026-08-18

## Status

Accepted.

## Context and Problem Statement

Dusk's MCP tools returned useful Markdown, but agents had to parse prose to recover entities, actions and outcomes.
Operational failures were often ordinary successful tool results, so a client could not distinguish a broken catalog read from an empty one.
The initialization instructions repeated details already present in tool descriptions, idle sessions lived forever, and an entity with pathological relation fan-out could consume an unbounded response.

Plugin actions had another information loss: Dusk flattened their JSON Schema to parameter names, discarding types, descriptions, enums, defaults and nested constraints.

## Considered Options

1. Preserve the fixed fat tools, pair their Markdown with structured content, signal operational failures with stable codes, return complete action schemas, expire idle sessions and bound graph lists.
2. Replace Markdown with JSON and require agents or clients to render it.
3. Create one tool per plugin action and one narrow tool per catalog field.
4. Keep prose-only compatibility and rely on agents to infer failures and schemas.

## Decision Outcome

Chosen: **option 1**.

Human-readable Markdown remains the primary content of every tool result.
Successful results also carry compact structured content for clients that can consume it.
Operational failures set MCP `isError` and carry a stable snake-case code alongside the message; expected absence remains a successful, explicit result.

Action discovery returns the plugin's complete JSON Schema unchanged.
Installing plugins still adds actions behind `get` and `invoke`, never new tools.

The portable server instructions contain only the cross-tool workflow that descriptions cannot carry alone.
Streamable HTTP sessions expire after 30 idle minutes by default, configurable with `DUSK_MCP_SESSION_TIMEOUT`.
Entity connection and dependent lists render at most 100 entries per section and state exactly how many were omitted.

## Consequences

### Good

- Agents can reason from Markdown while protocol clients use typed data without scraping prose.
- Empty and broken reads are distinguishable at the protocol boundary.
- Action calls receive the whole contract the plugin declared.
- Session and response cost have explicit bounds.
- Plugin growth does not enlarge the tool list.

### Bad

- MCP responses carry both representations, increasing wire bytes for clients that use only one.
- Stable error codes become a compatibility surface that must not be casually renamed.
- A graph result with more than 100 rows is intentionally incomplete and the caller must narrow its question.
- The shared structured envelope is less specific than a bespoke output schema for every tool.

### Rejected because

- **Option 2** was rejected because model-facing prose is part of Dusk's product and raw JSON makes common answers harder to read.
- **Option 3** was rejected because tool definitions are paid on every session and plugin count is unbounded ([ADR-0010](0010-mcp-surface.md), [ADR-0041](0041-plugins-reach-agents-as-actions.md)).
- **Option 4** was rejected because inference cannot recover schema fields Dusk discarded or reliably distinguish a failed read from an empty one.
