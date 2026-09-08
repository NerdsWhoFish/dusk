# 90. Share browser privacy policy as an independently bundled package

Date: 2026-09-08

## Status

Accepted.

## Context and Problem Statement

Several websites duplicate Faro setup and omit the privacy filters already implemented by Dusk.
Adding handled error capture independently would create drifting copies of the same policy.

## Considered Options

1. Keep an independently buildable package in the public Dusk repository
2. Create a separate repository and publishing infrastructure
3. Copy privacy filters into every website
4. Load a shared cross-site script at runtime

## Decision Outcome

Keep packages/browser-telemetry independent of the Dusk app and publish an npm tarball with Dusk releases. Consumers pin the immutable release asset and integrity hash, then bundle the SDK and policy into their own assets.

## Consequences

### Good

- One policy and real-SDK transport tests serve all consumers.
- Existing public repository and release workflow avoid new infrastructure.
- Each deployed application serves its own telemetry bundle.

### Bad

- Package maintenance shares Dusk repository and release ownership.
- Consumers must update the pinned artifact and redeploy to receive fixes.

### Rejected because

- A separate repository adds unnecessary ownership and publication infrastructure at the current size.
- Copies drift silently and require coordinated security fixes in every repository.
- A runtime cross-site dependency couples unrelated site availability and rollout behavior.
