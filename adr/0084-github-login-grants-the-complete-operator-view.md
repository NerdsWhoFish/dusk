# 84. GitHub login grants the complete operator view

Date: 2026-08-26

## Status

Accepted. Supersedes [ADR-0012](0012-viewing-auth.md), [ADR-0036](0036-deriving-what-a-viewer-sees.md), and [ADR-0051](0051-a-count-is-of-what-the-viewer-can-see.md).

## Context and Problem Statement

Dusk is built for one trusted homelab operator and their agents, but GitHub login currently creates a restricted per-repository view.
That filtered view hides observed infrastructure and operator tools, including Agent Context, and presents a silently incomplete product to the person operating it.
The shared bearer token grants the complete view, but requiring the operator to retrieve and paste that token makes the browser workflow worse than GitHub authentication.

## Considered Options

1. Keep deriving per-entity visibility from every repository the GitHub user can read
2. Grant complete operator access to any GitHub identity that completes OAuth
3. Admit GitHub identities that share at least one repository with the installed App, then grant the complete operator view
4. Add a Dusk-native user and role allowlist

## Decision Outcome

Chosen: **option 3**.

GitHub is the default browser login. A GitHub identity is admitted only when its user token can see at least one repository also visible to the installed App. Once admitted, that identity receives the same unrestricted catalog and mutation authority as the shared browser token.

The login page sends a configured deployment directly to GitHub. The token form remains an explicit fallback for recovery and for deployments without GitHub OAuth. Agents continue to use the bearer token.

Dusk no longer renders or serves a restricted GitHub viewer mode.

## Consequences

### Good

- The operator sees the same complete catalog and management surface regardless of whether they use GitHub or the shared token
- GitHub becomes the low-friction default without introducing users, roles, or grants inside Dusk
- An unrelated GitHub account cannot enter merely by completing OAuth because admission still requires overlap with the installed App
- The UI no longer needs to explain a silently filtered catalog or hide operator-only navigation

### Bad

- Any GitHub collaborator who can read one repository visible to the App receives full catalog read and mutation authority
- Per-repository multi-user views are removed, so one Dusk instance is explicitly unsuitable for mutually distrusting viewers
- Admission still asks GitHub which repositories the user can read, keeping GitHub reachable during first login
- The token fallback remains a second browser credential path that must stay tested

### Rejected because

- The filtered view was rejected because it contradicts Dusk's single-operator target and hides core operator workflows with no useful explanation
- Trusting any GitHub identity was rejected because a reachable OAuth callback would become a full-access door for unrelated accounts
- A native allowlist was rejected because it adds an authorization store and administration workflow solely to solve a single-operator login
