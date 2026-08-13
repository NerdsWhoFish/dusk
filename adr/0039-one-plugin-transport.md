# 39. gRPC is the only plugin transport

Date: 2026-08-13

## Status

Accepted. Supersedes the transport half of [ADR-0002](0002-plugin-protocol.md).

## Context and Problem Statement

[ADR-0002](0002-plugin-protocol.md) chose one protobuf schema expressed as two transports: protojson on stdout for batch ingesters, gRPC on a unix socket for anything long-lived.
It listed the cost itself, in its own Bad consequences: "two transports still means two code paths to implement and test, even though they share one schema."

Nothing has been built against either yet, so the cost has never been paid and the decision can still be changed for free.
Meanwhile the surrounding ADRs have accumulated requirements that the cheap tier cannot meet:

- [ADR-0020](0020-plugin-ui.md) has a plugin ship a Web Component, whose JavaScript Dusk fetches and serves from its own origin. That is a request Dusk initiates and a response carrying bytes.
- [ADR-0015](0015-plugin-actions-and-events.md) has Dusk invoke actions on a plugin and receive a result.
- Plugins should be able to stay resident and push what they observe, rather than being re-executed to be asked.
- Plugin authors must be able to work in any language, which is what the published `.proto` exists for.

Every one of those is Dusk talking *to* a plugin and getting an answer.
A process that prints JSON and exits can serve none of them.

The practical effect is that the cheap tier is only sufficient for a plugin that does nothing except emit entities, and any plugin worth writing eventually needs the other transport.
Two code paths are being carried so that the less capable one can be outgrown.

## Considered Options

1. **Keep both tiers**, as ADR-0002 decided.
2. **Consolidate on stdout protojson**, dropping gRPC.
3. **Consolidate on JSON-RPC over stdio**, the LSP and MCP pattern.
4. **Consolidate on gRPC over a unix socket.**
5. **Consolidate on gRPC over TCP, with plugins as network services** that dial Dusk from wherever they run.

## Decision Outcome

Chosen: **option 4**. One schema, one transport.

This is close to a deletion rather than an adoption.
`PluginService` is already published in `dusk-plugin-sdk` v1alpha1 with `Describe`, `ValidateConfig`, `Ingest`, `DryRun`, `Invoke` and `Status`, gRPC is already an indirect dependency, and the codegen already works.
Consolidating means removing the stdout tier, not building a new one.

### The requirements are already modelled

`Ingest` returns a **stream**, so a resident plugin holds it open and pushes as it observes rather than waiting to be asked again.
`Invoke` is [ADR-0015](0015-plugin-actions-and-events.md)'s actions.
`Describe` is where [ADR-0020](0020-plugin-ui.md)'s UI contributions are declared.
Dusk pulls with the unary calls and the plugin pushes up the stream, on one connection, without a second transport underneath.

### A unix socket rather than stdio

gRPC can be carried over a pipe pair, and in Go that is easy.
It is awkward in Python and Node, whose gRPC libraries expect a target address, and a transport that only Go implements comfortably would quietly undo the language-agnostic promise this protocol exists to keep.
Every major gRPC implementation accepts a `unix:` target.

### Why not the simpler wire format

Option 3 is the one worth taking seriously, and it is genuinely simpler to hand-roll: no sockets, no dependency, and the same pattern Dusk already runs for MCP.
It loses on the requirement it would be adopted to serve.
Carrying protojson as JSON-RPC parameters keeps the `.proto` as the schema but discards the generated service stubs, so every plugin author in every language rewrites method dispatch by hand.
That is a worse multi-language story than the one already published and working.

### The cheap tier is not replaced, it moves

[ADR-0002](0002-plugin-protocol.md) already named a floor beneath the protocol: "an integration can also skip the protocol entirely and just write files."
That floor is now the whole answer for the simple case.
Somebody who wants an entity in the catalog writes a `dusk.md`, which [ADR-0001](0001-git-as-source-of-truth.md) makes a first-class way in and which requires no binary, no protocol and no permission.

## Consequences

### Good

- One code path for one schema, which is ADR-0002's own second Bad consequence deleted rather than mitigated.
- Nothing is invented. The service, the generated code and the dependency all exist, so this is the cheapest it will ever be to decide.
- Assets, actions, streaming ingestion and Dusk-initiated reads are all expressible without adding a second mechanism later.
- Backpressure on a large batch is a property of the transport rather than a problem to solve, which the stdout tier never had an answer for.

### Bad

- **A plugin can no longer be a shell script.** That claim comes off the README, and it was a real differentiator. The zero-friction floor becomes "write a `dusk.md`", which is honest but is not the same promise.
- Every plugin now costs the host process lifecycle, health and cleanup. ADR-0002 kept a tier specifically to avoid that for the common case, and that escape is gone.
- `./my-plugin | jq` stops being how a plugin is debugged. The equivalent is grpcurl against a socket, which is a worse first five minutes for an author.
- A unix socket is one machine. Observing something Dusk cannot reach locally, such as a second cluster, has no answer here and is deliberately left open.
- **The proto is not finished.** Nothing in `PluginService` returns a Web Component's bytes, so ADR-0020 is currently unimplementable as written. v1alpha1 has to grow that call before it stabilises.

### Rejected because

- **Option 1** was rejected because the cheap tier cannot serve an asset, an action, or any request Dusk initiates, so every plugin that matters graduates out of it. Two code paths were being maintained for the case that is best served by writing a file instead.
- **Option 2** was rejected because a process that prints and exits cannot answer a question, which rules out [ADR-0015](0015-plugin-actions-and-events.md) and [ADR-0020](0020-plugin-ui.md) outright.
- **Option 3** was rejected because it discards working generated stubs and pushes hand-written dispatch onto every author in every language, weakening the multi-language promise it would be chosen to simplify.
- **Option 5** was rejected for now, not forever. It is the only option that solves observing a cluster whose credentials Dusk does not hold, by running the plugin where the credentials already are. It pays for that with authentication, registration, identity and NAT traversal before a plugin can emit a single entity, which is the ceremony Dusk exists to not have. If cross-network observation is wanted, the cheaper shape is an authenticated push endpoint that is not a plugin at all, and that decision has not been made.
