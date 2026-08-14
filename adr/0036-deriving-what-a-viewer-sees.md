# 36. Signing in with GitHub, and what it does not protect

Date: 2026-08-12

## Status

Accepted. Implements [ADR-0012](0012-viewing-auth.md).

## Context and Problem Statement

[ADR-0012](0012-viewing-auth.md) settled that a viewer signs in with GitHub and sees the entities owned by repositories they can read, so authorization is derived rather than configured.
It did not settle what happens to everything that has no repository behind it, nor what the shared token becomes once a second way in exists.

The deployment that exists today has one operator, one shared token, and a session cookie derived from it.
That token is not an identity: it is a password that grants the whole catalog, and every browser holding it is indistinguishable.

Three things had to be decided to make sign-in real.

**What an unauthenticated deployment does.** Most deployments are one person on a LAN, and requiring OAuth there would be ceremony protecting nothing.

**What observed entities are worth.** A Kubernetes Service has no repository, so repository access says nothing about who may see it. [ADR-0012](0012-viewing-auth.md) requires a declared visibility scope and no implicit default.

**Whether an invisible entity is absent or forbidden.** These are different answers and only one of them is safe.

## Considered Options

1. **OAuth replaces the token.** One way in, always an identity.
2. **OAuth alongside the token**, with the token remaining the unrestricted path.
3. **Token only.** Leave [ADR-0012](0012-viewing-auth.md) unimplemented until a second person exists.

## Decision Outcome

Chosen: **option 2**.

Signing in with GitHub is available when configured, and derives visibility from `GET /user/repos`.
A deployment that configures nothing keeps the shared token and sees everything.

### Unrestricted is the default, and that is the honest posture

With no OAuth configured, every viewer is unrestricted.
[ADR-0027](0027-design-target.md) designs for one operator and their agents, and a permission system that filters one person's view of their own infrastructure is theatre.

The filter engages the moment somebody signs in with GitHub, and not before.

### Observed entities are hidden by default

An entity an ingester found has no repository, so nothing about repository access answers who may see it.
It is hidden from a restricted viewer unless `DUSK_OBSERVED_VISIBLE_TO_ALL` says otherwise.

The asymmetry is deliberate: hiding something that should have been visible is a complaint, and showing something that should have been hidden is an incident.

### Invisible is indistinguishable from absent

An entity a viewer may not see returns the same 404 as one that does not exist.
Telling somebody that a thing exists but is none of their business leaks exactly the fact being protected, and "does not exist" is already what the catalog says about everything nobody declared.

### The shared token still grants everything

**This is the part worth being plain about.** `DUSK_MCP_TOKEN` is unchanged: anyone holding it, in a browser or an agent, sees the whole catalog regardless of what GitHub thinks.

That is correct for agents, which have no GitHub identity and are trusted by the operator who deployed them.
It also means OAuth is a way to give somebody a *narrower* view, not a way to keep somebody out.
A deployment wanting real multi-tenancy needs the token rotated out of every browser's reach first.

### Identity lives in memory

A restart signs everybody out.

Persisting sessions would mean storing a copy of GitHub's answer about who can read what, which is a cache of an authorization decision, and a stale one is exactly the drift [ADR-0012](0012-viewing-auth.md) chose derivation to avoid.

## Consequences

### Good

- [ADR-0012](0012-viewing-auth.md)'s central promise holds: there is no permission model inside Dusk to administer, and none that can drift from GitHub's.
- A single-operator deployment is unaffected, and nobody has to configure an identity provider to look at their own homelab.
- The dangerous default is the safe one. Observed entities stay hidden until somebody says otherwise.
- Filtering happens after the query, so an unrestricted viewer pays nothing for a feature they are not using.

### Bad

- **The shared token is a bypass, and it is the normal way in.** Sign-in narrows a view; it does not secure the deployment. Anybody who can read the token sees everything.
- Sessions in memory mean a redeploy signs everyone out, which is fine at one operator and irritating at ten.
- `repo` scope is requested to enumerate private repositories, which is far more access than reading a list of names needs. GitHub offers nothing narrower that still sees private repositories.
- Visibility is computed by listing every readable repository at sign-in, so somebody with a thousand repositories pays for twenty API calls and a list that goes stale until they sign in again.
- A restricted viewer's drift and integrity blocks are not filtered, so counts there can include things the viewer cannot open. Recorded as a gap rather than fixed. **Closed by [ADR-0048](0048-a-count-is-of-what-the-viewer-can-see.md)**, see the amendment below.

### Rejected because

- **Option 1** was rejected because it would make a single-operator LAN deployment require a GitHub App registration to look at its own catalog, and because agents have no GitHub identity to present.
- **Option 3** was rejected because [ADR-0012](0012-viewing-auth.md) is already accepted, and leaving it unimplemented while the UI grew would mean retrofitting a filter through every read later, which is how filters get missed.

## Amendments

### 2026-08-13: the unfiltered reports are filtered, and "filter after the query" gets a limit

The Bad consequence above is closed by [ADR-0048](0048-a-count-is-of-what-the-viewer-can-see.md), which also qualifies a claim made here.

"Filtering happens after the query, so an unrestricted viewer pays nothing" holds for a list and not for an aggregate.
A count cannot be made smaller after the fact without disagreeing with the list beside it, and a duplicate declaration is a count of copies, so narrowing the answer would report a duplicate while naming the repository holding the copy that made it one.
Drift, integrity and the kind counts therefore push the predicate into the query, and `index.Visibility` is a required argument on each of them.
An unrestricted viewer still pays nothing, because the clause they produce is empty.

### 2026-08-12: the registered App is the sign-in provider

"Available when configured" was read as "configured by hand", and sign-in took its client id and secret only from `DUSK_GITHUB_CLIENT_ID` and `DUSK_GITHUB_CLIENT_SECRET`.
Nothing set them, so the feature shipped unreachable and no test noticed, because the package had none.

Dusk already holds what it needs.
A GitHub App is an OAuth provider in its own right, the manifest in [ADR-0005](0005-github-app-and-access-modes.md)'s registration flow already claims `/auth/callback` as a callback URL, and the exchange returns a client id and secret that `store.Credentials` has been sealing on disk since the beginning.
Making the operator create a second app and copy in credentials Dusk is already holding was ceremony guarding nothing.

Sign-in now resolves credentials per call, preferring the environment and falling back to the registered App.
Resolving per call rather than at construction is the load-bearing part: the App is registered through `/setup` *after* the process starts, so a value read once at boot is empty for the lifetime of the pod that onboarded.

This also removes the `repo` scope in practice for most deployments, listed as a Bad consequence above.
A GitHub App ignores the scope parameter and grants what its installation already permits, so `/user/repos` returns the repositories the App can see and the person can read, which is a better answer than the one an OAuth App's `repo` scope buys.
The scope is still sent, because a deployment pointed at a genuine OAuth App through the environment still needs it.
