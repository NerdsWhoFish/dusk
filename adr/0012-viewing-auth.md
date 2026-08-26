# 12. Viewing authorization derives from GitHub repo access

Date: 2026-08-11

## Status

Superseded by [ADR-0084](0084-github-login-grants-the-complete-operator-view.md).

The authentication requirement and explicit trusted-network mode still stand. Repository-derived viewing authorization is replaced by ADR-0084.

## Context and Problem Statement

The GitHub App established in [ADR-0005](0005-github-app-and-access-modes.md) governs what Dusk can read and write.
It says nothing about who may read Dusk.

A catalog contains hostnames, internal architecture, ownership, and operational notes.
Rendering that on a URL without an answer to "who is allowed to see this" is not acceptable, and it is a gap that is easy to leave open until it is embarrassing.

The obvious approach is a permission system inside Dusk, with users, roles, and grants.
That is a second source of truth about access, and second sources of truth drift from the first.

## Considered Options

1. **A native permission model**: users, roles, and per-entity grants managed inside Dusk.
2. **OIDC or SAML SSO** against an external identity provider.
3. **GitHub OAuth**, with authorization derived from the viewer's access to the backing repositories.
4. **No authentication**, relying on network placement.

## Decision Outcome

Chosen: **option 3**, with option 4 available as an explicit, clearly labelled mode.

A viewer signs in with GitHub.
Dusk determines which of the repositories backing the catalog that person can read, and shows them the entities owned by those repositories.

Authorization is therefore **derived**, not configured.
There is no permission model to administer inside Dusk, and no possibility of Dusk's view of access drifting from GitHub's.

A trusted-network mode disables authentication entirely for LAN and single-operator deployments.
It is documented as exactly what it is, and is never the default.

### Ingester-emitted entities

Entities that come from ingesters have no backing repository, and therefore no natural access control.

The ingester's configuration must **declare a visibility scope**.
There is no implicit default, because silent over-sharing is a worse failure than a required field.

## Consequences

### Good

- No second permission model exists, so nothing can drift. Access is curated once, in GitHub, where the user is already curating it.
- Per-entity filtering falls out for free. An organization can run one Dusk instance and each person sees only what they already have access to, with nothing configured to make that true.
- The credential already exists. The App is installed, so there is no additional integration to build or operate.
- Revocation is immediate and needs no action in Dusk. Losing repo access loses catalog access.

### Bad

- Someone with no GitHub access to any backing repository sees an empty catalog. For a viewer-only stakeholder such as a manager or an auditor, that is surprising and will need either an explicit grant mechanism or a documented answer.
- Authorization checks depend on GitHub being reachable. This needs caching with a sensible staleness policy, and a decision about behaviour during a GitHub outage.
- Requiring ingesters to declare visibility adds a mandatory field and will be experienced as friction by plugin authors.
- Deriving authorization from repo access couples viewing to the VCS, which will need revisiting alongside any future non-GitHub source implementation.

### Rejected because

- A native permission model was rejected as a second source of truth. It would need administering, it would drift from GitHub, and it would reproduce work the user has already done.
- OIDC and SAML were deferred rather than rejected. They will be necessary for larger organizations, but building them first solves a problem no early user has while leaving the simple case unaddressed.
- No authentication was rejected as a default while being retained as an explicit mode, because the single-operator case is real and forcing an OAuth round trip on a LAN deployment is friction with no security benefit.
