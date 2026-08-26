# 82. AI search uses a bounded read-only estate agent

Date: 2026-08-25

## Status

Accepted. Supersedes [ADR-0081](0081-ai-search-is-grounded-and-opt-in.md).

The generic retrieval half is superseded by [ADR-0083](0083-search-fuses-exact-full-text-and-semantic-retrieval.md). The bounded agent, tool, visibility, disclosure, and read-only decisions still stand.

## Context and Problem Statement

The first AI search implementation converted a question into one FTS query and expanded a fixed graph slice before the model saw anything.
A tracing question proved that lexical mismatch and ranked-note fan-out could exclude the authoritative Grafana documents while stale context filled the budget.
The operator wants the model to investigate the estate itself and show exactly what it read.

## Considered Options

1. Use a bounded read-only estate agent
2. Improve the existing single-shot lexical grounding
3. Add embedding or vector retrieval
4. Expose the complete Dusk MCP tool surface to the model

## Decision Outcome

Chosen: **option 1**.

Give the configured OpenAI-compatible model two read-only tools: search the visible estate and read one visible entity or note by its exact identifier.
Search results carry identity and titles but no substantive body, so the model must read a document before relying on it.
Every provider round trip, search, document read, result page, individual document, and total returned context is bounded by the server.
The server resolves all tool calls against the same viewer-visible graph used by the UI, rejects unknown or hidden identifiers, deduplicates reads, and records search queries plus documents successfully read.
The final answer cites server-assigned source markers.
The API and UI disclose the search queries and every document read, even when the final prose does not cite all of them.
The agent receives no mutation, plugin, network, filesystem, shell, or generic MCP tools.

## Consequences

### Good

- The model can recover from vocabulary mismatch by trying alternate estate searches.
- A broad or stale first hit cannot consume the whole grounding budget before the model inspects competing documents.
- The user can audit both the searches performed and every document body supplied to the model.
- Viewer visibility and catalog-only grounding remain server-enforced rather than prompt-enforced.

### Bad

- A question may require several provider calls, increasing latency and subscription usage.
- Tool-call compatibility becomes part of the OpenAI-compatible provider contract.
- The fixed round and context budgets can still stop a difficult investigation before every useful document is read.
- The audit trail shows what was read, not whether the model interpreted every document correctly.

### Rejected because

- Improving one lexical query still makes the server guess the vocabulary and retrieval path before the model understands the question.
- Embeddings improve semantic recall but add a second index, model lifecycle, storage, and another visibility-sensitive retrieval path without enabling deliberate follow-up reads.
- The complete MCP surface includes mutations and operational integrations that are unnecessary for answering catalog questions and would greatly increase blast radius.
