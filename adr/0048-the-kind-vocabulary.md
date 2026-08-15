# 48. Kinds are derived, minted with a role, and never rejected

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

[ADR-0007](0007-entity-schema.md) made `kind` an open string rather than a protobuf enum, so that adding a kind is data instead of a release.
It also named the price in its own consequences: "Open strings mean typos become new kinds. This needs linting and a well-known-values list, or the taxonomy quietly fragments."

Nothing has done that linting.
`service`, `Service`, `svc` and `services` are four kinds today, the catalog splits four ways, and nothing anywhere says so.

[ADR-0010](0010-mcp-surface.md) sketched the answer as two tools, `getKinds` and `mintKind(namespace, name, aliases?)`, and left three things open.
Where a minted kind lives.
What minting does that declaring the same string does not already do.
And what happens when somebody declares a name that is nearly, but not quite, one that already exists.

The third is the one that decides whether any of this is worth building.
Refusing an unknown kind would delete [ADR-0007](0007-entity-schema.md)'s open vocabulary, which is the property that lets an operator describe their own estate in their own words.
Accepting every string silently is what produced the fragmentation.

There is a fourth constraint, from this repository rather than from the problem.
[ADR-0038](0038-what-drift-may-say.md) and [ADR-0045](0045-drift-is-a-maintenance-queue.md) both faced a choice between deriving a fact and asking the operator to maintain it, and both chose derivation, for the same reason: a hand-maintained list goes stale invisibly, and nothing indicates which direction it is wrong in.
A registry of kinds that has to be kept in step with what is declared would be exactly that failure mode, reintroduced.

## Considered Options

For where the vocabulary comes from:

1. **A configuration file listing every permitted kind**, maintained by the operator.
2. **Purely derived**, with the vocabulary being whatever is declared today and nothing else.
3. **Derived, plus a minted overlay carrying only what cannot be derived.**

For where a minted kind lives:

1. **A kind is an entity**, `kind:vocabulary/service`, declared like anything else.
2. **One file per minted kind**, the shape [ADR-0031](0031-notes-are-files.md) chose for notes.
3. **One file for the vocabulary**, `.dusk/kinds.md`, the shape [ADR-0013](0013-layout-and-pages.md) chose for the portal page.

For what happens on a near match:

1. **Reject the declaration** and name the kind that exists.
2. **Accept it silently.**
3. **Accept it and say what it nearly matched**, in the answer to the call that made it.

## Decision Outcome

Chosen: **derived plus a minted overlay**, in **one file**, with a near match **warned and never rejected**.

### The vocabulary is what is declared, plus what cannot be derived from it

A kind exists because something carries it.
`Vocabulary` counts entity kinds and note kinds out of the index, and that count is the vocabulary whether or not anybody has minted anything.
An operator who never calls `kinds` with a mint still gets a correct answer to "what kinds do I use", and it cannot go stale, because it is the same rows the rest of the catalog is read from.

Minting adds the two facts derivation cannot reach.

**A role**, because nothing in a string says what the thing it labels is for.
**Aliases**, because nothing anywhere can work out that `svc` means `service` without being told.

Both are stated once and are then true for every future declaration, which is the property a registry was reaching for without the staleness, since anything minted and unused is visibly unused: it reports a count of zero.

### Minting changes behaviour, or it is not worth doing

[ADR-0010](0010-mcp-surface.md) is explicit that "a purely decorative kind would be chosen carelessly and would be worthless".
So a mint is required to carry a role, and each role has exactly one consequence.

In the `note` namespace the role is `warning`, `knowledge` or `work`, and it decides ranking and rendering.
That half is [ADR-0049](0049-a-notes-kind-is-its-rank.md).

In the `entity` namespace the role is `infrastructure` or `reference`, and it decides whether drift expects the kind to be declared.
`infrastructure` is a thing somebody maintains, so something running and undeclared is a gap.
`reference` is a fact about the world, so it is not.

That second one is [ADR-0045](0045-drift-is-a-maintenance-queue.md)'s own option 4, arrived at from the other end.
That ADR wanted an ingester to say "airports are reference data and services are infrastructure", and rejected it on sequencing: it "needs an interface that does not exist, and it needs every plugin author to think about a question most will not".
Minting needs no plugin interface, and it asks the question of the operator, at the moment they are already deciding what a kind means.
The plugin-declared version stays the better answer for the case where the plugin author does know, and nothing here forecloses it.

The consequence is immediate and is the clearest demonstration of why minting exists at all.
An estate whose drift report opens with a hundred airports mints `airport` as `reference` once, and they are gone, without a filter, a suppression list, or a per-surface exception.

A kind nobody minted has the default role, which is `infrastructure` for entities and `knowledge` for notes.
So minting changes behaviour and not minting changes nothing, which is what makes this safe to add to a catalog that already exists.

### One file, because a vocabulary is not written the way notes are

`.dusk/kinds.md` carries the minted kinds in its frontmatter, in two lists, one per namespace, and its prose is whatever the operator wants to say about their own taxonomy.

[ADR-0031](0031-notes-are-files.md) rejected a single file for notes on contention, and the argument was specific: "Notes are written constantly, by agents, concurrently. Whatever holds them is the most contended file in the catalog."
That does not transfer.
A vocabulary is written rarely by construction, because a vocabulary that changes constantly is not one.
The file that this one resembles is [ADR-0013](0013-layout-and-pages.md)'s `.dusk/home.md`: singular, rare, and replaced as a whole.

It sits in `.dusk/`, which [ADR-0031](0031-notes-are-files.md) already keeps in scope everywhere, so a mint lands in a repository that declares no `include` and is still read back.

### `.dusk/` now holds two things, and a reconcile has to know which is which

`.dusk/` was Dusk's own directory for catalog content: notes, always in scope, discovered like anything else.
It has also, since [ADR-0013](0013-layout-and-pages.md), held `home.md`, which is not catalog content at all but Dusk's own declared configuration.

The reconciler globs `.dusk/**/*.md` and parses everything it finds as a note or an entity.
A declared homepage therefore fails to parse, and takes its whole repository's reconcile down with it, which is a live defect this decision found rather than introduced.
`kinds.md` would have joined it.

So the reserved paths are named in one place, `catalogfs`, whose job is already the file semantics of a catalog repository, and a reconcile skips them.
`page` and `vocab` take their path constants from there rather than each declaring their own and a third place listing both, which is the divergence this repository has had twice.

The cost is a small reserved namespace inside a directory that had none: a repository cannot have a note at `.dusk/home.md` or `.dusk/kinds.md`.

### A near match is a warning, in the answer, and never a refusal

`Nearest` compares a candidate against the vocabulary after case folding, removing separators, and dropping a trailing plural `s`, then by edit distance scaled to the length of the word.
`Service`, `services` and `serivce` all near-match `service`.

Where it fires:

- **Declaring** an entity, or recording a note, whose kind near-matches an existing one. The write happens, and the answer says what it nearly matched. This is where fragmentation actually occurs, because most kinds arrive through `declare` and never through a mint.
- **Minting**. Same warning, same non-refusal.
- **`dusk_context`** already lists every kind in use with its count, which is prevention rather than cure and costs nothing.

Refusing (option 1) would make Dusk a schema validator, and [ADR-0007](0007-entity-schema.md) explicitly refused to be one.
It would also be wrong often enough to matter, because `host` and `hosts` are a mistake while `service` and `services` might not be, and nothing outside the operator's head can tell.

The precedent for create-and-warn is in [ADR-0010](0010-mcp-surface.md) itself, one section below the one that names this problem: a near-identical note "is created with a warning naming the similar note".
This is that rule applied to kinds.

**A mint is refused in exactly one case**: when the name is already the vocabulary's after normalization.
Minting `Service` where `service` exists is not extending a vocabulary, it is putting two rows in it that mean the same thing, and the answer says to add it as an alias instead.
Declaring `Service:home/thing` still works, because a declaration is never refused.

Fuzzy matching cannot catch `svc`, whose edit distance from `service` is four.
Nothing can, without being told.
That is not a gap in the matcher, it is the reason `mintKind` takes aliases, and the two halves of this feature are the same mechanism seen from both ends.

### One tool, because the read is what authorizes the write

[ADR-0010](0010-mcp-surface.md) named two tools.
They ship as one, `kinds`, which reads with no `mint` and writes with one.

Since that ADR was written, `note` and `page` both landed in exactly this shape, for a reason that applies here unchanged: the read is what issues the proof token the write needs, so a separate read tool would look optional.
It is more than a convention here.
The proof token for a mint is proof of having read the vocabulary being extended, so the near-match warning is delivered by the very call that authorizes the mint, and an agent cannot invent `svc` without first having been shown `service`.
That is [ADR-0009](0009-proof-tokens.md)'s "you cannot write what you have not read" applied to a vocabulary, and it only works if the read and the write are one tool.

The tool count also matters on its own: [ADR-0010](0010-mcp-surface.md) makes the size of the surface a product constraint, and one tool is one, not two.

The vocabulary is treated as always existing, and a repository with no `kinds.md` has the empty one rather than none.
So a mint is always an update and never a create, which is what lets the first mint into a fresh catalog authorize the same way every later one does.
Recorded as an amendment on [ADR-0010](0010-mcp-surface.md).

## Consequences

### Good

- The vocabulary is right without being maintained, because it is a count over rows the catalog already holds.
- Minting is worth doing, because each role has a consequence somebody can see. A minted kind that changed nothing would have been ignored, and worse, would have taught agents that minting is ceremony.
- `reference` gives [ADR-0045](0045-drift-is-a-maintenance-queue.md) the precision it wanted without the plugin interface it was blocked on, and it is opt-in per kind rather than global.
- Nothing changes for a catalog that never mints, so this is additive on a live estate.
- A live reconcile defect is fixed on the way past: a repository declaring a homepage no longer fails to reconcile.
- The near-match rule has one implementation, in `vocab`, called from the three places that need it.

### Bad

- **A mint is only as good as the role somebody chose**, and a careless `infrastructure` on something that is really reference data puts noise back into drift. The role is a judgement and Dusk cannot check it.
- Two lists in one file means two agents minting at once collide on the blob sha. The collision is loud rather than silent, but it is a collision, and notes deliberately avoided this by being one file each.
- The warning arrives after the commit, so a fragmenting `declare` has already landed by the time anybody is told. Refusing would have prevented it and was rejected for a larger reason, but the cost is real: the catalog is momentarily wrong and somebody has to go back.
- Edit distance has no good answer for abbreviations, so the most common real fragmentation, `svc` against `service`, is caught only after somebody writes the alias.
- `.dusk/` now has reserved names, which is a rule with no enforcement beyond a reconcile quietly skipping the file. Somebody writing a note at `.dusk/kinds.md` gets no error, just a note nobody reads.
- The vocabulary is stored in its own write rather than inside the reconcile transaction, so a failure between them leaves a repository with no minted kinds until the next read. It degrades to the derived vocabulary, which is what an unminted catalog has, so it is safe rather than wrong. It is still two writes where the graph gets one.
- A mint reaches the catalog on the next reconcile, like every other write, so the role it declares is not live the instant the call returns.

### Rejected because

- **A configuration file of permitted kinds** was rejected for the reason [ADR-0038](0038-what-drift-may-say.md) rejected a configured coverage list: it goes stale silently and in both directions, suppressing kinds that are real and permitting kinds nobody uses, with nothing indicating which.
- **A purely derived vocabulary** was rejected because it cannot express a role or an alias, and those are the only two things that make a vocabulary more than a report. It is also unable to say anything about a kind before the first thing carrying it exists.
- **A kind as an entity** was rejected despite being the cheapest to build, since it needs no new storage, no new parser and no new plumbing. It puts the vocabulary in the entity graph, so kinds turn up in search results, in `dusk_context`'s inventory, in kind counts, and in every homepage block that queries entities. [ADR-0007](0007-entity-schema.md)'s entity is a thing in the estate, and a word is not one. The pollution is worst exactly where it is most expensive, in the context budget spent at the start of every session.
- **One file per minted kind** was rejected because the contention argument that made it right for notes does not apply, and it scatters something whose whole value is being read as a single list.
- **Rejecting an unknown or near-matching kind** was rejected because it deletes [ADR-0007](0007-entity-schema.md)'s open vocabulary, which is load-bearing for an operator describing their own estate. It also cannot distinguish a mistake from a deliberate distinction, and a false refusal is worse than a false warning: one blocks work, the other is ignored.
- **Accepting silently** was rejected because it is what the catalog does today, and it is the thing being fixed.

## Amendments

Amendment policy: [ADR-0028](0028-amending-adrs.md).

### 2026-08-15: the one refusal is a second spelling, not a name that exists

This ADR says "a mint is refused in exactly one case: when the name is already the vocabulary's after normalization".

That reads as one rule and is two, because the vocabulary it is checked against is the derived one.
Every kind anything carries is in it already, under the default role, so the refusal caught the case this ADR was written for: `airport`, minted as `reference`, refused because one airport is declared and therefore `airport` "already exists, for infrastructure".

The refusal now applies only to a name that normalizes onto a kind spelled differently, which is what the argument for it was always about.
The same spelling corrects the kind, keeping its aliases.

[ADR-0054](0054-correcting-a-kinds-role.md) records the reasoning, the options rejected with it, and the second thing this fixed: the refusal's own advice, "mint `service` with `Service` in its aliases", was itself refused.
