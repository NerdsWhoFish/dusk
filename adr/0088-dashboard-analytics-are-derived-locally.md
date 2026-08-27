# 88. Dashboard analytics are derived locally

Date: 2026-08-27

## Status

Accepted.

## Context and Problem Statement

The operator needs a useful dashboard that explains the shape and activity of their estate.
The source data contains private repository names, entity refs, notes, and infrastructure actions.
Measuring clicks or search text would create a new behavioral data store, while asking GitHub for file history would add API cost to a screen that should remain local and fast.
The dashboard needs to work with the data Dusk already owns and must state what each ranking actually measures.

## Considered Options

1. Derive a snapshot from the materialized catalog and bounded local action history
2. Record browser and agent interactions in a new local analytics store
3. Send product analytics to an external service

## Decision Outcome

Chosen: **option 1**.

Dusk exposes an `analytics` page block that derives its answer at read time from the current materialized catalog and the bounded action log.
Repository rankings measure how many current entities each declared source contributes.
Knowledge rankings measure how many entity refs a note connects, with open work and note-kind totals shown alongside them.
Plugin rankings measure invocations in the retained action window and include outcomes and last use.
The UI labels those measures plainly and labels the action window as bounded.
Dusk records no page views, search text, IP addresses, identities, or raw action parameters for this feature.
The computation performs no GitHub calls and sends nothing to an analytics provider.
The block is part of the page query vocabulary so an operator can place or omit it like every other dashboard block.

## Consequences

### Good

- Private catalog and behavior data never leaves Dusk
- The dashboard adds no GitHub API cost and remains available when GitHub is down
- No second durable store or retention policy is introduced
- Every number has an explainable local source and can be reproduced from current state
- Declared pages can position the analytics block without a UI-only customization path

### Bad

- Repository footprint is not the same thing as human attention and cannot be presented as page popularity
- Plugin usage covers only the bounded action history, so lifetime totals are intentionally unavailable
- The dashboard cannot rank notes by edit count because Dusk does not clone or retain Git history
- A large catalog adds local aggregate work to the home request
- A snapshot says what is true now and cannot draw a long-term trend line

### Rejected because

- Local interaction tracking was rejected because it creates sensitive behavioral state and retention semantics merely to produce vanity counts
- External analytics was rejected because catalog usage and infrastructure activity are private, and the requested value is for the operator rather than a vendor
- Fetching Git history for edit rankings was rejected because it adds GitHub API cost and network failure to a local dashboard
