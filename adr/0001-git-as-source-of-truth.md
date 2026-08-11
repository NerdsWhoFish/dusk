# 1. Git is the source of truth, and the index is keyed by ref

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk needs somewhere to keep entities: services, docs, gotchas, ownership, dependencies.
Two properties are non-negotiable.

First, **agents must be able to read it natively.**
An agent reading a markdown file from disk gets full fidelity, zero latency, no auth, and can grep the whole corpus in one call.
Anything that puts an API between the agent and the content is strictly worse for the primary consumer.

Second, **agent writes must be reviewable.**
An agent silently rewriting a knowledge base is how you get confidently wrong documentation.
Whatever stores the data has to support review, history, and blame without inventing a new trust model.

A third requirement emerged during design: the product must be able to render an unmerged pull request as the catalog *as it would be after merge*.
That requirement is cheap to satisfy at the start and expensive to retrofit.

## Considered Options

1. **Database as source of truth**, with markdown imported into it (the Confluence, Outline, Wiki.js model).
2. **Parallel YAML entity files** in each repo, with docs kept separately (the Backstage `catalog-info.yaml` model).
3. **Markdown with frontmatter in the repos being described**, with the index as a derived view keyed by git ref.

## Decision Outcome

Chosen: **option 3**.
Entities live as markdown with frontmatter in the repo that owns the thing being described.
The reconciler signature is `reconcile(ref)` from the first commit, never `reconcile()`.
The index is a derived materialized view, disposable and rebuildable at any time.

### Consequences

#### Good

- Agents keep direct file access. No API sits between them and the content.
- Metadata lives inside the doc it describes, so it rots at the same rate as the doc rather than faster.
- Agent writes become file edits, so existing review gates, `git diff`, `git log`, and PR review all work unchanged. No new trust model is required.
- PR previews are nearly free, because rendering `?pr=112` is just reconciling at that ref.
- The index can be deleted and rebuilt with no data loss, which makes schema migrations low-risk.

#### Bad

- Multiple live materialized views mean real memory and disk cost, plus a garbage collection path for closed PRs.
- Reconcile latency is bounded by git operations, so very large source repos will need shallow or partial clones.
- Ref-keyed storage is more complex than a single live state on day one, and that complexity is paid before its benefit is visible.
- Discovery from frontmatter means malformed or missing frontmatter is a real failure mode that needs good error surfaces.

#### Rejected because

- Option 1 fails the primary requirement. Content in a database is content agents cannot read natively and git cannot diff, and it discards history and review for free.
- Option 2 recreates the drift problem Dusk exists to solve. A parallel YAML file has no forcing function to stay accurate, so it rots faster than the docs it points at, and it adds a per-repo maintenance burden that falls on a human.
