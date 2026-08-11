# 29. Repositories are read over the API, at a pinned commit

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Reconciling needs the contents of a handful of files at a given git ref, from a repository Dusk does not have on disk.

The shape of that need is unusual and it drives everything here.
[ADR-0004](0004-dusk-md-convention.md) bounded the read surface to one root file plus whatever that file explicitly points at.
A typical repository therefore contributes **one file**, and a monorepo that has gone to the trouble of using `include` contributes perhaps thirty.

There is a second problem hiding behind the first, and it is the more dangerous one.
A ref is not a fixed thing.
`refs/heads/main` means a different tree after every push, so a reconcile that resolves the ref separately for each file it reads can assemble a graph from two different commits, with no error and no way to tell afterwards.
That is the quiet kind of wrong [`docs/philosophy.md`](../docs/philosophy.md) exists to forbid.

## Considered Options

For fetching:

1. **Clone with `go-git`**, then read the working tree.
2. **Shell out to `git`**, cloning or using a partial checkout.
3. **Read over the GitHub API.**

For consistency:

1. Pass the ref down and let each read resolve it.
2. **Resolve the ref to a commit once, and read everything at that commit.**

## Decision Outcome

Chosen: **option 3, reading over the API**, and **resolving to a commit once per reconcile**.

Three endpoints do the whole job: resolve a ref to a commit, list a tree, read a file's contents.

The `Source` interface gains `Resolve`, and the loader calls it before reading anything.
Every subsequent read is made against the returned commit, and that commit is recorded as provenance, so a claim in the catalog can be traced to the exact tree that produced it.

Cloning is the wrong shape for the requirement.
Fetching an entire repository, with its history, to read one markdown file is not a constant-factor inefficiency; it is orders of magnitude of network and disk to deliver a few kilobytes, repeated for every tracked repository and again for every pull request preview.
It also brings disk to manage, caches to invalidate, and concurrency to get right, none of which the API path has at all.

Pinning to a commit turns out to pay for itself immediately.
A commit's tree cannot change, so the tree listing that resolves `include` patterns is cacheable with no invalidation logic and no staleness window.
Correctness and the optimisation arrive in the same change, which is usually a sign the boundary is in the right place.

### Being GitHub-specific is contained, not avoided

These are GitHub endpoints, and that is a real cost paid deliberately.
[ADR-0005](0005-github-app-and-access-modes.md) already established GitHub first behind an interface that no provider type crosses, and the `Source` boundary is where that is enforced.
A different forge is a second implementation of three methods, not a change to the reconciler.

### Truncation must fail, never truncate

GitHub truncates a recursive tree listing for a very large repository.
Silently accepting a truncated listing would drop catalog files and produce a catalog that is confidently incomplete, which is worse than one that refused to load, so a truncated response is an error naming the repository.

## Consequences

### Good

- Reading costs roughly what the data is worth: one request to resolve, one to list, and one per catalog file.
- No disk, no clone cache, no working trees to garbage collect, and nothing to get wrong when two reconciles run at once.
- Installation tokens work directly against these endpoints, so authentication is the App credentials already stored, with no git transport to configure.
- Pull request previews cost the same as any other ref, which is what [ADR-0001](0001-git-as-source-of-truth.md) promised and cloning would have quietly made expensive.
- A pinned commit makes a reconcile reproducible and its provenance honest. Re-running against the same commit produces the same graph.
- The tree cache needs no invalidation, because it is keyed by something immutable.

### Bad

- Reads are rate limited, where a clone is one transfer. A repository contributing thirty entities costs thirty-two requests per reconcile, and the poll floor multiplies that by the number of tracked repositories.
- The recursive tree endpoint has a ceiling. A repository large enough to truncate cannot use `include` patterns at all, and the only advice Dusk can offer is to make the repository smaller, which is not advice.
- A catalog file is capped at a megabyte. The limit is arbitrary and will eventually surprise somebody, though a `dusk.md` that large is a different problem.
- Three GitHub endpoints are now load-bearing, and GitHub can change them. The pinned API version reduces this rather than removing it.
- Resolving adds a request to every reconcile, including the very common case of a repository that has not changed at all.
- `Resolve` is meaningless for a local directory, which has no commits. The local source returns the ref name in its place, which is honest but is not a commit, so provenance from a checkout is weaker than provenance from GitHub.

### Rejected because

- **Cloning with `go-git`** was rejected on proportionality and dependency weight. It is a large dependency added to fetch entire repositories in order to read one file from each, and it brings a disk cache whose invalidation is a genuine source of stale catalogs. It would become the right answer if Dusk ever needed history, blame, or bulk file access, and none of those are on the roadmap.
- **Shelling out to `git`** was rejected because it requires the binary in the container, which the distroless image deliberately does not have, and it still clones. It trades a Go dependency for a runtime one and keeps every disadvantage.
- **Resolving per read** was rejected because it is wrong rather than merely slower. It produces a torn graph silently, and the failure is undetectable after the fact.
