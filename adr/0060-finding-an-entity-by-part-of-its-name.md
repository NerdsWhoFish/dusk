# 60. An entity's own name is matched by substring, and its prose is not

Date: 2026-08-15

## Status

Accepted. Extends [ADR-0008](0008-storage.md) and [ADR-0049](0049-a-notes-kind-is-its-rank.md).

## Context and Problem Statement

Search is FTS5 over one table holding every entity's kind, name, title and description, and every note's kind and body ([ADR-0008](0008-storage.md)).
A query is turned into a match expression by quoting each word and making the last one a prefix, so typing narrows.

That is a word index, and infrastructure names are not words.
They are compounds an operator ran together: a host called `backupnas`, a service called `mediabox`, a box called `pihole`.
FTS5's default tokenizer splits on non-alphanumerics, so `backupnas` is one token, and `"nas"*` is a prefix match that never reaches inside it.

Driving the surface against a real catalog produced the failure this record exists for.
Searching for a three-letter host name returned sixteen hits, three of which were board cards matching a person's surname, and **not the host**, whose name ends with those three letters.
The host was found only because an unrelated snippet happened to quote its ref.

So the surface answered a question about a thing it holds with an answer that did not contain it, and the caller could not tell.
"Absence is explained, never silent" ([`docs/mcp.md`](../docs/mcp.md)) is about saying when there is nothing; this is the case where there is something and the index cannot reach it.

The obvious fix is the wrong one, which is why this is a record rather than a commit.
Substring matching over prose is worse than the problem: it finds "cat" in "concatenate", "ssh" in "flashing", and every relevance signal FTS5 has stops meaning anything.
The real question is narrower.
**Does an entity's own name deserve treatment its prose does not?**

## Considered Options

1. **Nothing.** Document that a compound name is matched by prefix, and let operators name things with separators.
2. **Retokenize the whole index as trigrams**, with `tokenize='trigram'`, so every column matches by substring.
3. **A second FTS table over identity only**, trigram tokenized, unioned into the search.
4. **A substring predicate over the `name` column**, evaluated alongside the match and ranked below it.
5. **Option 4 over the whole `ref`**, so a kind or namespace fragment also matches.

## Decision Outcome

Chosen: **option 4**.

A search runs the full-text match it always ran, and additionally finds entities whose **`name` column contains every word of the query**, excluding anything the match already returned.

### Because a name is an identifier and prose is prose

A name is the identity somebody chose for a thing, and it is the only field where a compound is normal rather than a mistake.
Nobody writes a description as one run-together word, and nobody searches for a fragment of a sentence.
So the treatment applies exactly where the failure is, and nowhere else.

That is the whole of the answer to "does a name deserve treatment its prose does not".
Yes, because a name is not text about the thing, it is the thing's handle, and a handle you cannot type half of is a handle.

### Ranked between a word hit and a work note

Three groups, ordered: a full-text hit, then a name hit, then a work note ([ADR-0049](0049-a-notes-kind-is-its-rank.md)).

A word match is a stronger signal than "your query appears inside this name", so a name hit never displaces one.
A name hit is a stronger signal than a todo, so [ADR-0049](0049-a-notes-kind-is-its-rank.md)'s rule that work ranks below every non-work hit still holds exactly.

Nothing is mixed into the relevance score, for [ADR-0049](0049-a-notes-kind-is-its-rank.md)'s reason: multiplying bm25 by a boolean produces an order nobody can reason about or test.
Within the name group the order is by ref, which is stable and arbitrary rather than pretending to a relevance it does not have.

### Words shorter than three characters are left alone

A two-letter substring is inside most of a catalog and carries no signal, where the prefix match a full-text query already does at least narrows.
So a query word under three characters contributes nothing to the name branch, and the full-text half answers alone.

### The name half is not free, and is bounded by what the catalog is

`instr` cannot use an index, so the name branch scans the entities table.
[ADR-0008](0008-storage.md) sized this deliberately: "the workload is thousands of entities, not millions".
A scan of thousands of short strings, in the same statement as a query that already ranks every full-text match, is not measurable.
If that assumption ever stops holding, option 3 is the upgrade path and this record is where to start.

## Consequences

### Good

- A thing the catalog holds is findable by the name it is actually called, which is the failure that produced this record.
- Prose keeps its word semantics, so nothing about ordinary search gets worse. That was the whole risk of the obvious fix.
- No schema change, no second index, no reconcile to rebuild anything. The index stays exactly as [ADR-0008](0008-storage.md) describes it.
- The rule is one sentence an operator can hold: your words are matched as words, and an entity's own name is also matched by substring.
- A name hit carries the entity's version like any other, so it authorizes a write the same way ([ADR-0009](0009-proof-tokens.md)).
- A name hit is one hit per ref, resolved the way `Get` resolves one, so an entity both declared and observed is found once rather than once per scope.

### Bad

- **A name hit has no snippet**, because `snippet()` is a full-text function and there is no match inside prose to quote. It renders as its title and ref alone, which is thinner than the hits above it.
- **`instr` scans.** The cost is bounded by the entities table rather than by the query, so a catalog that outgrows [ADR-0008](0008-storage.md)'s sizing pays on every search, including the ones that did not need it.
- Three characters is a threshold with a reason but not a principle, and somebody's two-letter service name is unfindable by half of itself.
- Two mechanisms now answer one question, and their results are ordered by which mechanism found them rather than by how well it matched. Somebody will read that as a bug.
- The name branch matches only entities. A note is found by its body, and its id is a path with a hash in it, so there is nothing there worth a substring.
- Case folding is SQLite's `lower`, which is ASCII only. A non-ASCII name is matched case-sensitively, and nothing says so at the call site.
- The two halves disagree about what a hit is. A name hit is one per ref, and a full-text hit is one per scope, so an entity both declared and observed is counted once when found by its name and twice when found by a word. That asymmetry is deliberate rather than good: not adding a second source of duplicates was in reach, and removing the first means deciding which copy's version a proof token records, which `Get` and `Locate` already answer differently. Recorded in [`docs/status.md`](../docs/status.md).

### Rejected because

- **Doing nothing** was rejected because the failure is silent. An operator who searches for their own host and does not find it concludes it is not in the catalog, which is the exact reasoning [ADR-0010](0010-mcp-surface.md)'s surface asks agents to trust.
- **Retokenizing the whole index as trigrams** was rejected because it makes prose search worse to fix a name problem: every relevance signal degrades, the index grows several times over, and prefix semantics change under every existing query. It is a large, irreversible cost paid in the wrong place.
- **A second trigram index over identity only** is the principled version of this decision and was rejected on proportion, not on correctness. It needs a virtual table, three triggers, a union across two ranked sources, and a reconcile to populate. It buys index-backed lookups the current catalog size does not need. It stays the upgrade path.
- **Matching the whole `ref` rather than the name** was rejected because a ref is `kind:namespace/name`, so a query naming a namespace would return every entity in it, and a query naming a kind would duplicate what the full-text index already answers. The flood is real: an operator whose namespace is `home` would get their whole catalog back for a common word.

## Amendments

None yet.
