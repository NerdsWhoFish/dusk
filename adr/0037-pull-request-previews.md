# 37. A pull request is a version of the catalog

Date: 2026-08-12

## Status

Accepted. Implements the review half of [ADR-0001](0001-git-as-source-of-truth.md).

## Context and Problem Statement

[ADR-0001](0001-git-as-source-of-truth.md) put the catalog in git so that changing it goes through review.
That is only true if a reviewer can see what a change does, and a diff of YAML frontmatter does not show that.

`kind: service` moving between two files is invisible in review and changes nothing.
Deleting six lines can remove an entity that a dozen notes are attached to, and looks identical.

[ADR-0008](0008-storage.md) keyed the index by git ref precisely so several versions could be live at once, and named pull request previews as the reason.
Nothing had used it.

Two questions had to be answered.

**What is compared.** A file diff is what GitHub already shows and is not worth repeating. A semantic diff describes the catalog after merge, which is the thing under review.

**Where a preview lives and when it goes away.** An index that accumulates a partition per pull request forever is a leak.

## Considered Options

1. **A status check.** Pass or fail, with detail behind a link.
2. **A comment with a semantic diff**, plus a browsable preview at the pull request's ref.
3. **A file diff annotated with catalog meaning**, as review comments on the changed lines.

## Decision Outcome

Chosen: **option 2**.

A `pull_request` event indexes the head commit under `refs/pull/<number>/head`, compares it to the default branch, and posts one comment.
Closing the pull request drops that ref.

### The diff is semantic, and provenance is excluded

The comparison is between entities, not files: added, removed, and per-field changes including attributes.

Provenance is deliberately skipped. Every entity's commit differs between any two refs, so including it would report a change on everything and bury the real ones.

The same reasoning excludes a reformatted file: if the catalog would say the same thing after merge, the comment says so plainly rather than listing churn.

### One comment, edited in place

The comment carries an invisible marker and is updated on every push rather than added to.
A review thread with fourteen bot comments is one nobody reads, and GitHub has no notion of "the comment I made last time".

### Removal is called out

A merged pull request that deletes an entity takes every note attached to it and everything anybody had written down.
Additions and modifications are listed; removals are listed **and** flagged in the summary line.

### The preview is browsable, and only previews are

`?ref=refs/pull/<number>/head` renders the whole UI against that version.
The query string accepts only a preview ref, so it cannot be used to wander into whatever else happens to be indexed.

### Closing is the cleanup

`DropGitRef` on close is the entire teardown, which is what keying by ref bought.
A pull request that is never closed keeps its preview, which is the correct behaviour and also the leak.

## Consequences

### Good

- A reviewer sees what a change does to the catalog, which is the thing [ADR-0001](0001-git-as-source-of-truth.md) put it in git for.
- The ref-keyed index finally earns its complexity.
- A no-op change says so, so the comment stays worth reading.
- Deletion, the one irreversible change, is the one that interrupts.
- Cleanup is one delete, and it is driven by an event GitHub already sends.

### Bad

- **Every open pull request holds a partition of the index.** A repository with fifty open pull requests holds fifty copies of its catalog, and nothing prunes an abandoned one until it closes.
- Each preview costs a tarball download and a full reconcile, on every push to the branch.
- The comment is posted with the App's identity, so a repository that restricts who may comment will see failures. These are logged and swallowed rather than failing the delivery.
- A preview is built before anybody asks for one, so most of the work is done for pull requests nobody looks at.
- A restricted viewer can browse a preview, and drift and integrity within it are unfiltered, so those counts can include entities they cannot open.

### Rejected because

- **Option 1** was rejected because pass or fail is the wrong shape: a catalog change is almost never wrong, it is something to understand, and a check that always passes is one nobody reads.
- **Option 3** was rejected because the interesting changes have no line to attach to. An entity disappearing because its file was deleted has no line in the diff to comment on, and that is exactly the change worth flagging.
