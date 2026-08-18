# 70. Mutations have durable retry identity

Date: 2026-08-18

## Status

Accepted.

## Context and Problem Statement

A mutating plugin call can change its target and lose the reply before Dusk sees it.
Calling it again may repeat the change, while recording the severed call as failed falsely says it did not happen.

Asynchronous actions had a related split: the browser could poll a returned handle, MCP could not, and the event was marked successful before the handle finished.
The bounded in-memory event buffer also disappeared on restart, taking the only record of what ran with it.

## Considered Options

1. Give every mutating invocation a caller-supplied idempotency key, reserve it durably before calling the plugin, and keep events and handle links in a local durable journal.
2. Require every plugin and every target API to implement idempotency independently.
3. Retry transport failures automatically and continue treating a lost plugin as a failed action.
4. Export events to an external time-series system and keep Dusk stateless.

## Decision Outcome

Chosen: **option 1**.

Mutating actions require an idempotency key.
Dusk fingerprints the intended action, persists an unknown reservation before invoking it, and returns the remembered result when the same key and request arrive again.
Reusing a key for a different request is refused.

If a mutating plugin disappears after the call begins, the outcome and event are `unknown`, not failed.
The reservation remains unknown until reality or a durable plugin result proves more.

Asynchronous handles remain attached to the event that started them.
MCP polls them through the fixed `invoke` tool by passing `plugin` and `handle`, and the event settles only when status says the work is done.

The bounded journal lives at `actions.json` in Dusk's data directory.
It contains recent events, retry receipts, and asynchronous handle links and is loaded before actions are served.

Plugin configuration uses the same read-before-write posture.
The browser and MCP receive a content version and proof token, and the manager compares the version again under its lifecycle lock before selecting a candidate configuration.

## Consequences

### Good

- A lost reply cannot silently run the same intended mutation twice.
- Unknown is represented honestly and survives a restart.
- Browser and agent action semantics are the same.
- Recent action history no longer vanishes during a rollout.
- Concurrent plugin configuration edits cannot overwrite each other.

### Bad

- Callers must create and retain an idempotency key for every intended mutation.
- The local journal is bounded operational history, not an immutable compliance audit log.
- Dusk serializes the admission and receipt window for invocations so simultaneous retries cannot both miss.
- A corrupt journal stops startup because discarding retry identity is less safe than refusing actions.

### Rejected because

- **Option 2** was rejected because plugin consistency is not an end-to-end retry guarantee and many homelab APIs have no idempotency primitive.
- **Option 3** was rejected because retrying an unknown mutation is the failure this decision prevents.
- **Option 4** was rejected because a self-contained homelab appliance must remain safe without another database, and an exporter cannot protect the retry that happens before it receives an event.
