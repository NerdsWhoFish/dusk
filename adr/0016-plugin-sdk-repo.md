# 16. The plugin contract and SDK live in their own repository

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

[ADR-0002](0002-plugin-protocol.md) establishes a published `.proto` as the single contract every plugin compiles against, and [ADR-0015](0015-plugin-actions-and-events.md) extends it with actions.

Where that contract lives is a distribution decision with consequences.

Keeping it inside the server repository means a plugin author depends on the entire Dusk server to build a plugin that only needs a message definition.
It also ties the contract's version to the server's, and it signals that the contract is an internal detail rather than a public API.

## Considered Options

1. **Inside the server repository**, published as a Go package path within it.
2. **Its own repository**, holding the `.proto` and an SDK.

## Decision Outcome

Chosen: **option 2**.

A separate repository holds, in order of importance:

1. **The `.proto`.** This is the actual contract. A plugin author working in Python, Rust, or TypeScript takes only this.
2. **A Go SDK** as a convenience layer for the common case.
3. **Conformance tests** a plugin can run against itself.

The protocol version is negotiated at handshake, and a documented compatibility matrix states which SDK versions work with which server versions.

## Consequences

### Good

- A plugin author depends on a message definition, not on a server.
- The contract versions independently of the server, which matters because the server will change far more often than the protocol should.
- A separate repository signals that the contract is a public API, which is the message the plugin ecosystem needs in order to exist.
- Putting the `.proto` first keeps non-Go authors as first-class citizens rather than an afterthought, which is what makes the language-agnostic promise in ADR-0002 real.
- Conformance tests give a plugin author a definitive answer to "is my plugin correct" without reading the server.

### Bad

- Two repositories means version skew is now a real thing to manage, documented, and to get wrong.
- A change spanning the protocol and the server becomes two pull requests in two repositories, which is friction on exactly the changes most likely to need coordination.
- Release processes multiply, and the compatibility matrix is a document that will go stale unless it is generated.
- Early on, when the protocol is changing rapidly under `v1alpha`, the split costs more than it returns.

### Rejected because

- Keeping the contract in the server repository was rejected because it makes the contract look internal, couples its version to the server's, and forces plugin authors to depend on far more than they need.
