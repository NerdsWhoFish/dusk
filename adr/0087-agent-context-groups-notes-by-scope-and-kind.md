# 87. Agent context groups notes by scope and kind

Date: 2026-08-27

## Status

Accepted.

## Context and Problem Statement

A flat pinned-note stream makes unrelated kinds run together and hides which entries are full context versus pointers.
The browser preview and MCP answer share the renderer, so their hierarchy and ordering must remain one contract.

## Considered Options

1. Keep one flat note list in catalog order
2. Group notes by kind while preserving catalog kind order
3. Group notes by scope and kind, ordering full-body kinds before collapsed kinds

## Decision Outcome

Chosen: **option 3**.

Keep repository-relevant notes under `Notes` and estate-wide notes under `Global Notes`. Inside each scope, add one heading per note kind. Order the groups whose bodies are configured to print in full before groups that collapse to title and `note(id)`, while preserving first-seen kind order within each partition.

## Consequences

### Good

- Agents can scan the knowledge estate by scope and kind.
- The context spends attention on bodies already present before pointing at follow-up reads.
- Changing a kind's full-body policy automatically changes its relative placement without another ordering configuration.

### Bad

- Kind headings consume part of the context budget.
- Changing full-body policy also reorders groups.
- The rendered order no longer exactly follows catalog note order across kinds.

### Rejected because

- A flat list is cheaper by a few heading bytes but keeps the hierarchy the operator explicitly asked to expose.
- Catalog kind order groups knowledge but can bury expanded bodies below collapsed pointer groups.
