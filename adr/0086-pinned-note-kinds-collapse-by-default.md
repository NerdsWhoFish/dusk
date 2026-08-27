# 86. Pinned note kinds collapse by default

Date: 2026-08-26

## Status

Accepted.

## Context and Problem Statement

Pinned notes currently use repository attachment as a proxy for whether to print a title or the full body.
That makes behavior depend on refs instead of what the note kind means, and a newly minted kind can silently consume the initial context budget.
The operator needs kind-level control shared by the MCP renderer and browser preview.

## Considered Options

1. Collapse notes according to whether they have repository refs.
2. Maintain an allowlist of kinds whose notes collapse.
3. Collapse every kind by default and configure only the kinds whose full bodies should be printed.

## Decision Outcome

Chosen: **option 3**.

Add a `full_note_kinds` exception list to the Git-backed context profile. Every pinned note kind renders as a title with a nested `note({ id: ... })` call unless its kind appears in that list.
The default profile lists `reference`, `todo`, and `idea`. `gotcha`, `incident`, `runbook`, `howto`, `decision`, `project`, and every newly minted kind remain collapsed without needing code changes.
Use this policy for both repository and estate note sections through the shared renderer.

## Consequences

### Good

- New note kinds are token-safe without a deployment or profile edit.
- The operator can choose full-body behavior by kind in the existing Git-backed policy.
- MCP output and the browser preview cannot drift because both use one renderer.

### Bad

- A new kind whose body genuinely belongs in initial context stays collapsed until the operator adds an exception.
- Existing profiles must inherit default exceptions when the field is omitted without making an explicitly empty list impossible.
- Changing the exception list still waits for the config repository to reconcile.

### Rejected because

- Repository refs describe what a note is about, not whether its body deserves context budget, so this keeps producing surprising output.
- An allowlist makes every new minted kind full-body by default, which is the unsafe direction for a bounded context.
