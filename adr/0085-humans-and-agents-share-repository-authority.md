# 85. Humans and agents share repository authority

Date: 2026-08-26

## Status

Accepted.

## Context and Problem Statement

Dusk is one operator's memory, and losing knowledge because a repository was never opted in is the product failure to avoid.
The browser can expose repository curation, but agents are primary operators too and must not be denied the same safe Git-backed action.
Creating root dusk.md was previously treated as a human-only consent boundary even after the GitHub App installation had already established repository authority.

## Considered Options

1. Keep repository opt-in human-only, while agents may edit only repositories already in the catalog
2. Build separate browser and MCP implementations for repository opt-in
3. Give both callers one proof-gated root dusk.md read and write capability

## Decision Outcome

Chosen: **option 3**.

Choose option 3.

A repository capability reads the exact root `dusk.md`, or reports its absence with an explicit editable starter. The read issues a repository-specific proof token. Supplying the complete file with that token validates it as a root declaration, then creates or replaces it through the existing access-mode-aware Git write path.

The browser API and MCP tool call the same writer methods. GitHub App installation access remains the authority boundary. Proposal mode remains a proposal, write mode remains a commit, and stale reads are refused. Creating the well-known file is an ordinary non-destructive write and does not require a separate confirmation.

Humans and agents have equal product authority. A safe, proof-gated Git-backed action exposed to either surface must be available to the other unless the surface cannot represent it.

## Consequences

### Good

- Agents can preserve knowledge by opting repositories in instead of stopping at an artificial human-only boundary
- Humans and agents exercise the same validation, proof, access-mode, and commit behavior
- The editable starter makes the file shape discoverable without silently hiding deployment-specific defaults
- One writer implementation prevents UI and MCP behavior from drifting

### Bad

- The fixed MCP surface grows by one tool, which spends context in every agent session
- An agent with GitHub App access can create a new tracked file in a repository that had no Dusk declaration
- Whole-file replacement makes the proof token and fresh Git read load-bearing against concurrent edits
- The generated starter derives namespace and name from GitHub identity, so a caller may need to edit those visible values before writing

### Rejected because

- Rejected because GitHub App installation already grants repository authority, and a second human-only consent rule causes undocumented systems and lost knowledge without adding a real security boundary.
- Rejected because duplicated mutation paths drift in validation, proof handling, proposal behavior, and error recovery, producing unequal authority in practice.
- Chosen.
