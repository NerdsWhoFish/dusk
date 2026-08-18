# 67. Catalog writes have explicit inverses

Date: 2026-08-18

## Status

Accepted.

## Context and Problem Statement

Dusk can add a declaration, set fields on it, and add an outbound relation.
It cannot undo any of those operations.

That makes `drift` and `integrity` observation tools rather than maintenance queues.
An agent can find a stale declaration, a bad attribute, a wrong alias, a dangling edge, or a duplicate declaration and still has to tell the operator to edit YAML by hand.

Removal needs a stricter answer than addition.
Deleting a declaration file drops its outbound relations and removes the entity from normal reads, while a decommissioned entity is useful history and should remain searchable without being reported as missing from reality.
Duplicate declarations add another constraint: the ref alone does not identify which file should change.

## Considered Options

1. Add separate `unset`, `unrelate`, `decommission`, and `remove` tools.
2. Add explicit inverse operations to `declare` and `relate`.
3. Let agents edit the files directly and keep the MCP surface additive only.

## Decision Outcome

Chosen: **option 2**.

`declare` remains the owner of one entity declaration.
It gains:

- `unset`, naming `title`, `description`, `observed_as`, or `attributes.<name>`
- `observed_as`, replacing the alias set when present
- `decommissioned`, where true sets `lifecycle: decommissioned` and false removes it
- `repository`, which selects one declaration when the same ref is declared more than once
- `remove`, guarded by `confirm`, which deletes an included declaration file

`relate` remains the owner of one exact edge, identified by `from`, `type`, and `to`.
It gains relation attributes, `unset` for those attributes, and `remove` guarded by `confirm`.

### Decommissioning preserves knowledge

A decommission is a declaration update, not a deletion.
Dusk stores it as the conventional `lifecycle` attribute with value `decommissioned`, keeps it searchable, renders the state with the rest of its attributes, and excludes it from the declared-not-observed drift queue.

The attribute is used instead of a new protocol field because lifecycle is authored catalog policy rather than a fact every plugin must emit.
Promoting it into the plugin contract would make every plugin version and every emitted entity carry a field whose meaning is only needed for declarations.

Setting `decommissioned: false` removes the conventional attribute rather than setting it to `active`.
Absence remains the active default and old files do not need migration.

### Removal is narrow and confirmed

Only an included file can be removed.
Deleting the root `dusk.md` would opt the entire repository out and remove every entity it includes, so `declare` refuses to do it under the name of one entity.

`remove: true` also requires `confirm: true`.
The proof token establishes that the caller read the exact current declaration; confirmation establishes that it meant to delete rather than decommission it.
Read mode still returns the deletion as a diff.

### A field is changed only when named

The existing merge rule remains load-bearing.
Absent inputs leave the file alone, an `unset` entry removes exactly one named field, and an explicitly present empty `observed_as` replaces the alias list with an empty one.

Unknown unset paths are refused rather than ignored.
A write that claims to correct a field and changes nothing is worse than one that says the request is malformed.

### Duplicate declarations are addressed by source

`get` accepts an optional repository selector and returns that exact declared copy.
Its proof token records that copy's version.
Passing the same repository to `declare` routes the update or removal to that file.

The selector does not change a ref's identity and is not a second ref syntax.
It is only the disambiguator `integrity` needs when two files already violate the one-declaration rule.

## Consequences

### Good

- Every actionable drift or integrity class now leads to a supported write rather than a hand-edit instruction.
- Decommissioned systems keep their runbooks, history, and relations without polluting the missing queue.
- A duplicate can be fixed without relying on the same repository sort order that made it a problem.
- The MCP tool count stays fixed and the inverse sits beside the operation it reverses.

### Bad

- `declare` and `relate` have more modes, so their schemas and answers need stronger compatibility tests.
- `lifecycle` becomes a reserved semantic inside the otherwise open attribute map.
- Removing a declaration leaves attached notes pointing at nothing until the caller moves or closes them; `drift` reports them rather than deleting knowledge on the caller's behalf.
- A root declaration still needs a direct file edit or a repository-level workflow to remove, because deleting it is larger than an entity operation.

### Rejected because

- **Option 1** was rejected because four inverse tools double the write surface and separate the operation from the schema and proof rules it must exactly mirror.
- **Option 3** was rejected because it makes the maintenance queue stop at the point where maintenance begins, and teaches agents a second write path with different proof, access-mode, and collision behavior.
- A tombstone note was rejected because a note has no entity ref of its own, so ordinary `get`, relations, and attached knowledge would all stop resolving.
- Deleting every entity called `decommissioned` was rejected because retirement is historical state, not absence.
