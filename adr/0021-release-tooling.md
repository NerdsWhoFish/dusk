# 21. Release with docker buildx and helm directly, not GoReleaser

Date: 2026-08-11

## Status

Accepted. The chart-publishing portions are **superseded by [ADR-0024](0024-charts-publishes-charts.md)**: Dusk publishes only the image, and `NerdsWhoFish/charts` publishes charts itself.

## Context and Problem Statement

Dusk releases two artifacts: a multi-architecture container image and a Helm chart in a separate repository, per [ADR-0019](0019-chart-repo.md).

GoReleaser is the default answer for releasing Go projects and is already used elsewhere in these repositories, so not using it needs a reason.

Its strengths are archives, checksums, signing, changelogs, and package manager publishing.
Dusk ships none of those.
There is no binary archive to publish, no Homebrew tap, and no `go install` path that matters, because Dusk is a service rather than a CLI.

For the one thing Dusk does ship, GoReleaser adds a step rather than removing one.
Either it builds per-architecture binaries that a Dockerfile then copies, which splits build logic across two files, or it thinly wraps `docker buildx` and the abstraction earns nothing.

## Considered Options

1. **GoReleaser** with its Docker support.
2. **`docker buildx` and `helm` invoked directly** from the workflow.

## Decision Outcome

Chosen: **option 2**.

The workflow calls `docker buildx build` for the multi-architecture image and `helm package` with `helm push` for the chart.
There is no `.goreleaser.yaml`.

The release safety patterns are kept regardless of tooling, because they are about not shipping a broken tag rather than about GoReleaser:

- `workflow_dispatch` with `scope` and `bump` choice inputs, so releases are deliberate.
- Pull requests run a snapshot build, so a cross-build break is caught before merge rather than at release.
- Concurrency that cancels superseded pull request builds but never cancels a release mid-tag.
- A refusal to release from anything other than `main`.
- **A full dry-run build before tagging.** A tag is only worth cutting once the thing is known to build.
- **Untag on failure.** A tag left behind by a failed run burns that version number permanently.

Two Dusk-specific additions:

- **Chart packaging happens before tagging**, because it is side-effect free. A broken chart therefore never burns a version number.
- **`helm push` happens after the image push**, and if it fails the image is already public. That fails loudly and is deliberately not rolled back, because deleting a published image is worse than a chart being one version behind.

Cross-compilation uses `--platform=$BUILDPLATFORM` with `GOOS`/`GOARCH`, so Go cross-compiles natively instead of building under QEMU emulation.

## Consequences

### Good

- One tool per artifact, each doing what it is for, with no configuration file translating between them.
- Build logic lives in the Dockerfile alone rather than split between a Dockerfile and a GoReleaser config.
- `arm64` is a first-class target rather than a matrix entry, which matters because that is what these clusters run.
- Native cross-compilation is dramatically faster than emulated builds and removes an entire class of QEMU-specific failure.
- All the release safety patterns are retained, since none of them depended on GoReleaser.

### Bad

- Changelog generation, checksums, and signing are now absent rather than free. If any of those become necessary, they are hand-rolled or GoReleaser comes back.
- Two release pipelines now exist across these repositories with different tooling, so a fix to one does not carry to the other.
- `docker buildx` invocations in YAML are less readable than declarative configuration, and the workflow is longer as a result.
- If Dusk ever ships a companion CLI, this decision needs revisiting rather than extending.

### Rejected because

- GoReleaser was rejected on fit rather than on quality. Nearly all of what it provides is for artifacts Dusk does not produce, and for the one artifact it does, it wraps the tool that would otherwise be called directly.
