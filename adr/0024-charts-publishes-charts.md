# 24. The charts repository publishes its own charts

Date: 2026-08-11

## Status

Accepted. Supersedes the release-coordination half of [ADR-0019](0019-chart-repo.md).

## Context and Problem Statement

[ADR-0019](0019-chart-repo.md) moved the Helm chart into `NerdsWhoFish/charts` but kept the release driven from the Dusk repository, on the reasoning that **chart versions must track the application version** so that a chart version always corresponds to a real image.

That requirement was invented rather than discovered, and it is not the Helm convention.

Helm already separates the two ideas.
`version` is the chart's own version and `appVersion` records which application version the chart targets.
Every major chart repository works this way, and consumers already read `appVersion` for exactly this question.

Insisting the two move in lockstep bought nothing and cost a great deal:

- A GitHub App to create, install, and maintain, plus two secrets to hold and rotate.
- A cross-repo write in the release path, which is a step that can fail on its own.
- A failure mode where the image is published and the chart is not, requiring a loud failure with no rollback.
- A chart-only fix having to wait for a full application release, which ADR-0019 recorded as a downside without recognising it as a symptom of the wrong design.

## Considered Options

1. **Keep the coupling**, as ADR-0019 decided.
2. **Each repository publishes its own artifacts.**

## Decision Outcome

Chosen: **option 2**.

- `NerdsWhoFish/dusk` publishes the container image and nothing else.
- `NerdsWhoFish/charts` publishes charts on its own schedule, with its own release workflow.
- **Chart version and `appVersion` are independent.** `appVersion` is set at chart release time to whichever Dusk version that chart targets.

No GitHub App, no `CHARTS_APP_*` secrets, no cross-repo write anywhere in either pipeline.

Each workflow uses only its own `GITHUB_TOKEN`.

The charts release keeps the same safety shape: `workflow_dispatch` with a chart and bump selector, pull requests lint and render every chart on both default and non-default values paths, a dry-run package before tagging, and untag-on-failure.

### Nothing keeps them in sync automatically, and that is fine

A Dusk release does not trigger a chart release.
Most Dusk releases need no chart change, and the ones that do (a new required value, a new environment variable) are a deliberate chart pull request regardless.

Where a chart lags meaningfully, `appVersion` says so plainly, which is the question a consumer actually asks.

## Consequences

### Good

- Two independent pipelines, each publishing what it owns, with no coordination to get wrong.
- No App, no long-lived secrets, and nothing to rotate.
- The cross-repo failure mode is gone rather than documented.
- A chart-only fix ships immediately instead of waiting for an application release it has nothing to do with.
- It matches what every Helm consumer already expects, so `appVersion` means the conventional thing.

### Bad

- The chart can lag the application, and nothing catches it automatically. A release adding a required value ships an image no published chart can deploy until someone notices.
- Two release processes exist where there was one, and they can be operated inconsistently.
- Reproducing a historical deployment now needs two versions, chart and application, rather than one.
- Chart tags live alongside no application tags in a repository with a different numbering scheme, which is more to hold in your head.

### Rejected because

- Keeping the coupling was rejected because the requirement behind it was manufactured. It imposed real infrastructure, real secrets, and a real failure mode to enforce a property Helm already expresses with `appVersion`.
