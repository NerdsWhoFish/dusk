# 89. Catalog footprint excludes config repositories by declared role

Date: 2026-08-27

## Status

Accepted.

## Context and Problem Statement

Catalog footprint ranks declared repositories by the number of distinct entities they contribute.
A config repository holds entities that have no better codebase, so its high count reflects filing location rather than an estate source worth comparing.
The repository already identifies itself through an entity carrying `role: config-repository`.

## Considered Options

1. Exclude repositories whose own declared entity carries `role: config-repository`.
2. Exclude the configured `DUSK_CONFIG_REPOSITORY` value.
3. Keep every declared repository in the ranking.

## Decision Outcome

Chosen: **option 1**.

Catalog footprint omits any repository whose current default view contains a declared entity with the attribute `role: config-repository`. The exclusion is derived in the catalog query, before ranking and limiting, so an excluded repository cannot consume one of the bounded result slots. Current entity totals still describe the complete catalog.

## Consequences

### Good

- The filing repository no longer dominates an estate comparison with a technically true but operationally misleading count.
- The rule is portable across operators and repository renames.
- The exclusion is declared in catalog data and can be inspected through the same entity surfaces as other behavior.

### Bad

- Repository aggregation now includes a JSON attribute predicate and a correlated exclusion query.
- A missing or misspelled role leaves the config repository visible.
- The repository source count and the total entity count intentionally describe slightly different scopes.

### Rejected because

- The configured repository value is convenient but makes process configuration, rather than catalog meaning, decide what the dashboard calls a config repository.
- Keeping every repository preserves a raw count but repeats the misleading ranking that prompted this decision.
