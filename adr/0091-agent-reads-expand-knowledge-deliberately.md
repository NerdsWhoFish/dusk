# 91. Agent reads expand knowledge deliberately

Date: 2026-10-05

## Status

Accepted. Supersedes [ADR-0059](0059-what-a-list-may-not-leave-unsaid.md).

## Context and Problem Statement

Session audits found excessive onboarding output. The titles flag hid Markdown bodies but leaked every full body in structuredContent, and an oversized first note exceeded the budget. Mandatory startup requirements such as tracing must remain visible.

## Considered Options

1. Default to indexes in both representations with explicit full reads and operator-owned startup policy.
2. Keep full default reads and raise output or model limits.
3. Remove structuredContent or truncate responses indiscriminately.

## Decision Outcome

Chosen: **option 1**.

get and note lists default to summaries with IDs, kinds, status, refs and provenance. Explicit titles:false includes only complete bodies that fit the existing byte budget; note(id) returns the complete selected note. Both representations share one projection. Entity indexes page with get(note_offset); note lists page with note(offset), each capped at 100 with totals and continuation. The manual directs agents to relevant warnings; mandatory policy remains operator-configured in .dusk/context.md. ADR-0074's dual-representation contract remains intact at the requested detail level; clients should render one representation. The context preview counts returned Markdown using an embedded o200k_base tokenizer and labels it an estimate excluding other model inputs.

## Consequences

### Good

- Summary responses cannot leak unrequested note bodies.
- Mandatory startup policy remains explicit and detailed knowledge stays retrievable.
- Response token cost is visible without external requests or private-content telemetry.

### Bad

- Clients expecting default note bodies must request titles:false or note(id).
- Agents must follow applicable warnings before acting.
- Embedded vocabulary increases binary size and tokenization can differ from the client's model.

### Rejected because

- Raising limits delays compaction without removing irrelevant or duplicate output.
- Removing a representation breaks clients that read only that half; arbitrary truncation hides warnings.
