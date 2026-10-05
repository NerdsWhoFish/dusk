# 92. Repository context refresh is explicit and stateless

Date: 2026-10-05

## Status

Accepted.

## Context and Problem Statement

Agents moving between repositories in one conversation need fresh repository warnings and declarations, but repeating operator policy, global notes, inventory, and the manual spends context without adding information.
Mandatory startup policy, including tracing and privacy, must remain recoverable after a new conversation or loss of context.
An MCP transport session may serve several conversations and is not evidence that a model still holds an earlier response.

## Considered Options

1. Explicit startup and repository modes with unchanged full startup default.
2. Always return full startup context for every repository refresh.
3. Deduplicate automatically using server-side MCP session state.

## Decision Outcome

Chosen: **option 1**.

Add dusk_context(root, mode), where mode is startup by default or repository. Startup preserves the complete existing renderer and returns full policy on every explicit request, including repeated calls in the same MCP session. Repository mode requires a nonempty exact owner/name and returns only repository declarations and repository-relevant pinned notes, with bounded overflow read handles. It omits operator instructions, global pins, estate inventory, and the manual. An unknown valid repository is reported as unknown without falling back to global context; invalid modes and missing repository roots are errors.
The Agent context UI offers Full startup and Repository refresh, defaulting to startup. GET /api/context accepts the same mode and root and uses the same renderer; responses identify the selected mode. Tracing records only the validated fixed mode enum, without adding private context to telemetry.
Clients load startup once per conversation, use repository mode for later repository changes, and request startup again when its policy was lost or intentionally needs refreshing. Compaction alone is not proof of loss. The server does not infer conversational memory or suppress explicit startup requests. This extends ADR-0091 and preserves ADR-0069 operator policy and ADR-0074 dual-representation behavior.
A complete startup payload injected by the optional SessionStart hook satisfies the initial load; agents must not repeat the call merely because a hook delivered it. Generic AGENTS instructions or a shared MCP connection do not establish that the complete payload was loaded. The hook uses the explicit lifecycle source to skip automatic reloads for resume, compact, and fork, which carry conversation history. It loads full startup for startup, clear, absent, and unknown sources. This is a client lifecycle optimization, not server session deduplication: agents still request startup explicitly when mandatory policy is missing or stale. Compaction alone does not prove policy was lost.

## Consequences

### Good

- Repository changes avoid repeating unrelated startup content while retaining repository warnings and declarations.
- New conversations and lost context can reliably recover mandatory tracing and privacy requirements.
- Stateless selection works across shared MCP sessions, reconnects, and client implementations; browser previews show the same chosen response.
- Hook-provided startup and retained conversation history avoid redundant automatic onboarding.

### Bad

- Clients must choose the appropriate mode; repository refresh alone is insufficient onboarding.
- Operator policy may become stale during a long conversation until the client explicitly refreshes startup.
- Repository refresh intentionally omits global warnings, so clients must retain startup context and read applicable full notes.
- A resumed, compacted, or forked conversation that lost mandatory policy requires an explicit startup request; the hook cannot inspect model memory.

### Rejected because

- Always-full responses waste context on repeated operator-wide content when only repository scope changes.
- Session-state deduplication mistakes a transport identifier for model memory and can hide mandatory policy from a new or compacted conversation.
