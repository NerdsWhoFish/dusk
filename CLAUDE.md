# Dusk

A service catalog that maintains itself.
Read [DESIGN.md](DESIGN.md) before writing code, and [docs/philosophy.md](docs/philosophy.md) for the posture behind it.

## Before you change anything

1. **Read [`docs/status.md`](docs/status.md).** It is the checklist of what is built and what is not, and it is the fastest way to know where the work stopped.
2. **Read [`adr/README.md`](adr/README.md).** The decisions are already settled, with the rejected alternatives recorded. Most "why is it like this" questions are answered there.
3. **Read [ADR-0017](adr/0017-engineering-policy.md).** It is the engineering policy: Go conventions, package layout, testing, documentation, and the cgo rule.
4. **Read [`docs/packages.md`](docs/packages.md) before writing anything new.** It says what each package is for, what it is *not* for, and where a given kind of code belongs. Reach for it every time you are about to add a helper, a rule, or a package.

If something looks wrong, check whether an ADR already argued it before proposing a change.
Settled decisions are not re-litigated without new information.

## ADRs are a first-class tool here

Any decision with a real trade-off gets an ADR: an alternative that looked better than it was, a non-obvious constraint, anything a future reader would otherwise re-argue.

- They live in `adr/`, numbered sequentially, in [MADR](https://adr.github.io/madr/) form.
- **Amend in place, supersede a rewrite.** Any part of an ADR may be corrected, recorded in a dated `## Amendments` section at the bottom of the file. The limit is scale: rewriting its major sections is a new ADR that supersedes the old one, which stays ([ADR-0028](adr/0028-amending-adrs.md)).
- **A decision that no longer applies is `Retired`, never deleted.** Superseded means something replaced it, retired means nothing did. The file always stays, because its rejected alternatives are what stop the argument being had again.
- **Every ADR lists its rejected options and its bad consequences.** One with no downsides has not been thought through.
- **Adding, superseding, or retiring an ADR updates [`adr/README.md`](adr/README.md) in the same change.** A stale index sends readers to decisions that do not exist.
- Any rule an ADR calls load-bearing gets a test named after it: `TestADR0011_FailedIngestDoesNotDelete`.

Propose an ADR when you spot a decision that warrants one. Do not wait to be asked.

## Non-negotiables

These are the ones most likely to be broken by accident.

- **No cgo without an ADR.** `make nocgo` enforces it. cgo usually arrives transitively and takes cross-compilation, distroless images, and arm64 with it.
- **A failing ingester never deletes.** "I could not look" and "it is not there" must never be the same thing ([ADR-0011](adr/0011-ingester-scheduling.md)).
- **The poll floor stays.** It looks redundant next to webhooks and is not; removing it makes the catalog go silently stale ([ADR-0006](adr/0006-reconcile-triggering.md)).
- **Events never go in SQLite.** That index is disposable by contract and events cannot be rebuilt from git ([ADR-0015](adr/0015-plugin-actions-and-events.md)).
- **Every write needs a proof token.** You cannot write what you have not read ([ADR-0009](adr/0009-proof-tokens.md)).
- **State the API cost of anything that touches GitHub.** An installation gets ~5,000 requests an hour for every repository it can see, most of which declare nothing. Per-file reads, ungated sweeps, and anything scaling with how much a repository declares are the failure mode. Exhausting the budget makes the catalog *wrong*, not slow ([ADR-0017](adr/0017-engineering-policy.md)).
- **Plugins normalize; Dusk never re-derives** ([ADR-0018](adr/0018-normalization-at-the-edge.md)).
- **One concept, one owner.** Before writing a rule, a matcher, a parser, or a helper, find the package whose job it already is in [`docs/packages.md`](docs/packages.md). A second implementation never fails loudly; it drifts until a local check disagrees with production, which has happened here twice.

## Working here

```bash
make check   # lint + nocgo + test, what CI runs
make test    # go test -race ./...
```

- **Tests assert observable results**, are table-driven by default, and use the standard library only. No testify, no go-cmp.
- **Document in `docs/`**, one markdown file per subsystem, one sentence per line. Doc comments on every exported identifier.
- **Comment implementation with restraint.** Documentation and commentary are different things: a missing doc comment is a defect, an inline comment restating the code is noise.
- **`internal/` needs a reason.** Default to a real package in `pkg/` that another caller could use.
- **Adding, renaming, deleting, or meaningfully rescoping a package updates [`docs/packages.md`](docs/packages.md) in the same change.** A stale map sends the next person to reimplement something that exists.
- Commit as you go, in atomic commits with conventional messages. Do not push or tag without being asked.
- **Update [`docs/status.md`](docs/status.md) in the same change that moves an item.**
- **Anything deferred gets a line in [`docs/status.md`](docs/status.md), in the change that defers it.** Choosing not to build something now is fine. Leaving no trace of the choice is not, because "later" and "forgotten" look identical from outside. A `[ ]` item, a `[~]` with what is missing, or a Known gaps entry: whichever fits, but one of them, written while the decision is fresh enough to explain.
- **This repository is public.** Never commit anything about a particular person's deployment: hostnames, cluster names, domains, or the names of private repositories. Examples use `example.com` and generic names.

## Plugin repositories

A plugin lives in its own repository named `dusk-plugin-<name>`, because that prefix is how the marketplace finds it ([ADR-0042](adr/0042-installing-plugins.md)). It follows this repository's conventions: ADRs in `adr/`, one sentence per line, tests on observable results, `make check` as what CI runs.

**A plugin does not write its own process lifecycle.** `plugin.Run` in the SDK binds the socket, requires the host's token, and shuts down politely. That code was copied byte-for-byte into three repositories before it lived in one, and a convention duplicated per plugin is one that drifts per plugin. The same goes for checking what a plugin declares: `conformance.ValidateDescribe` is a test every plugin should have, so a wrong description fails in the plugin rather than later in Dusk.

Two things differ, both because a plugin ships a binary and Dusk ships a service:

- **Every plugin releases with GoReleaser**, with release notes generated from conventional commits, and the same dispatch-with-scope-and-bump flow this repository uses ([ADR-0021](adr/0021-release-tooling.md)). Dusk itself deliberately does not use GoReleaser: it publishes a container image, so GoReleaser would only wrap `docker buildx` and has nothing else to do. That reasoning does not transfer to a plugin, which publishes per-platform binaries that Dusk downloads from a GitHub release. Do not "fix" either repository to match the other.
- **The release workflow is called, not copied.** It lives once in `NerdsWhoFish/.github` and every plugin references it with `uses:`. Copied CI drifts silently, and a marketplace whose plugins each package themselves slightly differently is one where installing is different every time.

Because a release artifact is what Dusk downloads and executes, the checksums GoReleaser produces are not decoration. [ADR-0042](adr/0042-installing-plugins.md) verifies them on install.
