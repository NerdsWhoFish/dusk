# 32. Reads download a tarball, and only for repositories that opted in

Date: 2026-08-11

## Status

Accepted. Supersedes the read half of [ADR-0029](0029-reading-repositories.md).

## Context and Problem Statement

[ADR-0029](0029-reading-repositories.md) chose the API over cloning for reads, and it was right on the evidence it had.
Its argument rested on one premise, stated plainly: [ADR-0004](0004-dusk-md-convention.md) bounds the read surface, so "a typical repository therefore contributes **one file**".
Fetching a whole repository to read one file is disproportionate, so three endpoints beat a clone.

That premise no longer holds, and the ADR named the condition itself:

> It would become the right answer if Dusk ever needed history, blame, or **bulk file access**, and none of those are on the roadmap.

Bulk file access arrived from three directions at once.
[ADR-0031](0031-notes-are-files.md) makes every note its own file, and notes are what agents write most.
`.dusk/` is read whenever it exists, so a participating repository has files to enumerate whether or not it declares an `include`.
And pointing `include` at an existing documentation tree, which is the thing that makes Dusk useful against a repository nobody prepared, turns one repository into hundreds of files.

There is a measured cost as well as a predicted one.
A sweep of 89 repositories, of which one participates, spends roughly 180 calls per pass to discover that 88 of them still have no `dusk.md`.
At a ten minute floor that is a fifth of the hourly budget spent learning nothing, before any repository does anything interesting.

## Considered Options

1. **Keep reading a file at a time**, and accept the call count.
2. **Clone each participating repository**, fetching incrementally on a push.
3. **Download a tarball of the tree** at the commit being reconciled.

## Decision Outcome

Chosen: **option 3**, with two gates in front of it.

A reconcile resolves the ref to a commit, and **stops there if the commit has not moved** since the last successful reconcile of that repository.
An unchanged repository costs exactly one call, which is the irreducible price of noticing that nothing happened.

If the commit has moved, Dusk asks whether the repository has a root `dusk.md`.
**A repository without one is never downloaded.** It has not opted in, so there is nothing to read and no reason to transfer anything.

Only then is the tarball fetched, extracted to a temporary directory, read in full, and thrown away.

**Writes stay on the API.** Committing one file through the contents endpoint is one call and needs no working tree, no credentials in a checkout, and no merge. Nothing about writes was wrong.

### Why a tarball rather than a clone

A clone is incremental where a tarball is not, which is a real advantage and the reason option 2 is not obviously wrong.

It loses on everything else that matters here.
A clone brings a cache: disk that grows, state that goes stale or corrupt, concurrent access to serialise, and a lifecycle to garbage collect.
A tarball has none of that, because there is nothing to keep. Download, extract, read, delete.
That is the property [ADR-0029](0029-reading-repositories.md) was actually protecting when it rejected cloning, and it survives here while the call count does not.

It also costs no dependency.
`go-git` is large, and putting the `git` binary in the image means abandoning distroless.
A tarball is `archive/tar` and `compress/gzip` from the standard library.

The non-incremental cost is bounded by the gates: a full tree is transferred only when a participating repository actually changed, which is precisely when all of it needs re-reading anyway.

### What it fixes for free

The recursive tree endpoint truncates for very large repositories, which Dusk had to detect and refuse.
Walking an extracted directory has no such limit, and `**` globs become a directory walk rather than a pattern matched against a paginated listing.

## Consequences

### Good

- A steady-state sweep costs one call per repository instead of one per repository plus one per file. Nothing changed means nothing downloaded.
- A repository that has not opted in is never transferred, so the cost of watching it is a single ref resolution.
- Reading a hundred files costs the same as reading one, which is what makes documentation trees and per-file notes affordable at all.
- No cache, no disk that grows, no invalidation, no corruption, and no concurrency question, because nothing outlives the reconcile.
- No new dependency, and the image stays distroless.
- Tree truncation stops being a failure mode, and recursive globs become trivial.

### Bad

- A one-line change to a large repository transfers the whole tree. A clone would have sent a delta. This is the clear loss, and the repository that makes it hurt is the signal to revisit option 2.
- Extraction is attacker-influenced input the moment an allowlisted account is compromised, so it needs a size cap, a file count cap, and path checks. A per-file read had none of that surface.
- Temporary disk is needed during a reconcile, proportional to the largest repository rather than to what is actually read.
- Reconciling now has three phases where it had one, and a bug in the skip logic would present as a catalog that has quietly stopped updating, which is the failure mode this project fears most.
- Two mechanisms now talk to GitHub for content: tarballs for reads, the contents API for writes. They can disagree about what the tree looks like, and a write reads the file again through the API rather than from the tarball it just extracted.

### Rejected because

- **Option 1** was rejected because its founding premise is gone. It remains the cheapest way to read exactly one file, and if Dusk only ever read `dusk.md` it would still be correct.
- **Option 2** was rejected on lifecycle and dependency weight rather than on transfer volume, where it wins. It becomes right when repositories are large enough or change often enough that full-tree transfers dominate, and the `Source` boundary means adopting it is one implementation rather than a change to the reconciler.

## Amendments

### 2026-08-11: what ADR-0004 promises, narrowed

This decision changed the scope of [ADR-0004](0004-dusk-md-convention.md)'s central promise and did not say so.

That ADR reads "the root file plus whatever it explicitly points at, and nothing else in the repository is ever read".
Under a tarball that is no longer literally true: every markdown file in a participating repository is transferred, and the include list decides what is *parsed* out of it.
The named test asserting the rule was written against the old reading and passed only because the old reader made one request per declared path.

The promise still holds where it matters, and the narrowing is stated here rather than left implicit:

- **Consent is unchanged.** A repository still opts in by containing a `dusk.md`, and one that does not is never downloaded at all.
- **What Dusk publishes is unchanged.** Only declared paths are parsed, indexed, searched, or served to an agent. An undeclared file is discarded when the reconcile ends.
- **What crosses the wire is wider.** A participating repository's other markdown is transferred and held in memory for the duration of one reconcile.

The test is now `TestADR0004_OnlyDeclaredPathsEnterTheCatalog` and asserts the graph rather than the transfer, which is the guarantee that survives.

An operator who needs the stricter reading needs option 2, whose delta transfer is the only shape that gives it back.

### 2026-08-11: one implementation of the file semantics

The `Source` interface asked each reader for `ReadFile` and `Glob`, so each carried its own matching.
Three existed and they disagreed: two supported `**` and filtered to markdown, the third did neither, so `dusk validate` and the server resolved the same `include` differently and a repository could pass locally and be read wrong in production.

`Source` now asks only for `Resolve` and `Tree`.
Matching, reading and the markdown rule live on the tree in `pkg/catalogfs`, so a reader's whole job is producing one and no reader can drift from the spec.
The API-based reader is deleted rather than left as a fourth.
