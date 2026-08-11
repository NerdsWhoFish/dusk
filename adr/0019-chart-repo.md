# 19. The Helm chart lives in its own repository

Date: 2026-08-11

## Status

Accepted. The release-coordination half is **superseded by [ADR-0024](0024-charts-publishes-charts.md)**.

The decision to give the chart its own repository stands.
The decision to drive its release from the Dusk repository does not: chart version and `appVersion` are independent, and each repository publishes what it owns.

## Context and Problem Statement

Dusk ships as a container image and a Helm chart.
The chart can live beside the application code or in a repository of its own, and the choice determines how releases are coordinated.

Colocation is the obvious default.
The chart and the application version together, a single pull request changes both, and there is no cross-repo anything.

It has costs that arrive later.
A chart repository is a discovery surface: people look for `<org>/charts` and expect to find every chart there, not scattered across product repositories.
A second FetchHQ project shipping a chart would either duplicate the pattern or force a migration.
And chart consumers watching for chart changes end up watching a repository whose traffic is almost entirely application commits.

## Considered Options

1. **Colocated**, at `charts/dusk` inside the Dusk repository.
2. **Its own repository**, `FetchHQ/charts`, holding every FetchHQ chart with Dusk's at `dusk/`.

## Decision Outcome

Chosen: **option 2**.

`FetchHQ/charts` holds all charts, with Dusk's at `dusk/`.
Charts are published as OCI artifacts to `ghcr.io/fetchhq/charts/<name>`, so there is no repository index to host or keep current.

### The release still drives from the Dusk repo

**Chart versions track the application version.** Releasing Dusk `v1.2.3` publishes chart `1.2.3` with `appVersion: 1.2.3` pointing at image `v1.2.3`.

Splitting the repositories does not split the release.
The Dusk release workflow, after tagging and pushing the image, writes the chart version into `FetchHQ/charts` and publishes it.

That cross-repo write uses a **GitHub App token scoped to the charts repository**, minted per run via `actions/create-github-app-token`, rather than a personal access token.
It reaches exactly one repository and expires within the hour, where a PAT would sit in settings until somebody remembered to rotate it.

This is the standard mechanism for a cross-repository write from CI, and it is preferred to a long-lived token for the same reasons everywhere else.

### A chart-only fix still gets a full version

Chart versions correspond to application versions or they correspond to nothing.
A chart `1.2.4` with no matching application release is unresolvable six months later, so a chart-only fix rides the next release.

## Consequences

### Good

- `FetchHQ/charts` is where people will look, and it scales to a second and third chart with no migration.
- Chart consumers can watch a repository whose changes are actually about charts.
- OCI publishing means no chart index to host, no `gh-pages` branch, and no stale index file.
- A scoped, expiring App token is a materially better credential than a PAT for a cross-repo write, and the pattern is already proven in another repository.
- Keeping the release driven from Dusk means the chart cannot silently drift to a version no image was built for.

### Bad

- A change touching both the application and its chart is now two pull requests in two repositories, and they can land out of order.
- The release workflow gains a cross-repo write, which is a step that can fail on its own. If the chart publish fails after the image is pushed, the image is out and the chart is behind, and that has to fail loudly rather than roll back.
- Two more secrets to hold and eventually rotate, and an App to keep installed.
- Testing a chart change against an unreleased application build is more awkward than it was when both were one checkout.
- Requiring chart-only fixes to ride a full release means a trivial template typo waits for the next application version.

### Rejected because

- Colocation was rejected on discovery and scaling rather than on mechanics. It is genuinely simpler today, and it makes the second chart a migration rather than a directory.
