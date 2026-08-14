# Kinds

A kind is an open string ([ADR-0007](../adr/0007-entity-schema.md)).
`service`, `host`, `datastore`, or anything else you want to call the things you have.
Nothing validates one, and nothing ever will: an operator describing their own estate in their own words is the point.

The price of that is fragmentation.
`service`, `Service`, `svc` and `services` are four kinds nobody meant, the catalog splits four ways, and nothing says so.
The vocabulary is what makes that visible, and minting is what gives a kind a meaning something acts on ([ADR-0048](../adr/0048-the-kind-vocabulary.md)).

## The vocabulary is counted, not configured

Ask for it with the `kinds` tool.

Most of it is derived: every kind that anything carries is in the list, with how many things carry it.
There is no registry to keep in step with the catalog, because it *is* the catalog, counted.

Two things cannot be counted, and those are what minting adds.

**A role**, because nothing in the string `airport` says whether an airport is something you maintain or a fact about the world.
**Aliases**, because nothing anywhere can work out that `svc` means `service` without being told.

There are two vocabularies, in two namespaces: `entity` and `note`.
The note one is seeded with `gotcha`, `runbook`, `howto`, `decision`, `incident`, `todo` and `idea`.

## Roles, and what each one does

A mint has to carry a role.
Every role changes behaviour, because a kind that only labelled would be chosen carelessly and would be worth nothing ([ADR-0010](../adr/0010-mcp-surface.md)).

**Entity kinds:**

| Role | What it means | What it changes |
| --- | --- | --- |
| `infrastructure` | Something you maintain | One running and undeclared is reported by `drift` |
| `reference` | A fact about the world | `drift` stays quiet about it |

`reference` is the answer to a drift report that opens with a hundred airports.
Nobody is ever going to write a `dusk.md` for Boston Logan, so `airport` is minted as reference once and they stop being work ([ADR-0045](../adr/0045-drift-is-a-maintenance-queue.md)).

**Note kinds:**

| Role | What it means | What it changes |
| --- | --- | --- |
| `warning` | Something that will bite you | First on the thing it is attached to |
| `knowledge` | How a thing works, or why | Between the two |
| `work` | Something not done yet | Can be closed with a status, and never outranks anything in a search |

The seeded kinds split cleanly: `gotcha` and `incident` are warnings, `runbook`, `howto` and `decision` are knowledge, `todo` and `idea` are work.

A kind nobody minted has the default role, `infrastructure` for entities and `knowledge` for notes.
So minting changes behaviour and not minting changes nothing, which is what makes this safe to add to a catalog that already exists.

## What a role does to ranking

A note's kind is its rank ([ADR-0049](../adr/0049-a-notes-kind-is-its-rank.md)).

On an entity, and in what `get` returns, notes come out pinned first, then by role, then by id.
A gotcha reaches the top of a service's page with nobody pinning it, which is what pinning was previously being spent on.

In search, a work note ranks below every other hit and by relevance within that group.
A todo can never outrank a gotcha or an entity, and on a busy search it falls off the end.
Nothing is hidden, so a quiet search still finds it.

Two orderings exist and it is worth knowing which is which.
`get` and an entity page rank by role, because the question is "what should I know about this".
The `note` tool and the homepage's recent-notes block stay in recency order, because the question there is "what is new".

## Minting

Read the vocabulary, then mint against the proof token that read returned.

```text
kinds()
kinds(namespace: "entity", mint: "airport", role: "reference", proof: "…")
kinds(namespace: "note", mint: "postmortem", role: "warning", aliases: ["post-mortem"])
```

The token has to come from the `kinds` read specifically, not from any read ([ADR-0009](../adr/0009-proof-tokens.md)).
That is what makes the near-match warning load-bearing rather than advisory: an agent cannot invent `svc` without having been shown `service` first.

A repository with no vocabulary file has the empty vocabulary rather than none, so the first mint authorizes the same way every later one does.

Minted kinds land in `.dusk/kinds.md` in the config repository:

```markdown
---
dusk: v1alpha1
entities:
  - name: airport
    role: reference
    aliases:
      - aerodrome
notes:
  - name: postmortem
    role: warning
---

Airports are reference data. Nobody is going to declare Boston Logan.
```

The prose is yours and is written through untouched.
A mint reaches the catalog on the next reconcile, like every other write.

**Cost:** reading the vocabulary costs one file read against GitHub, the same as reading the portal page, because the token has to match the file a write will land on rather than the indexed copy of it.

## Near matches warn, and never refuse

A kind close to one that exists is reported, in the answer to the call that created it.
The write always happens.

- **Declaring** an entity whose kind nearly matches one that exists.
  This is where fragmentation actually occurs, because most kinds arrive through `declare` and never through a mint.
- **Recording a note** with a nearly-matching kind.
- **Minting**, where the same warning comes back with the mint.

Matching folds case and separators, then allows a small edit distance scaled to the length of the word, counting a transposition as one edit.
`Service`, `services`, `serivce` and `hostname` all near-match something.

It cannot reach `svc`, which is four edits from `service`.
Nothing can, without being told, and that is what aliases are for.
The two halves of this are the same mechanism seen from both ends.

**A mint is refused in exactly one case**: the name is already the vocabulary's after folding case and separators.
Minting `Service` where `service` exists is not extending a vocabulary, it is putting two rows in it that mean the same thing, and the answer says to add it as an alias instead.
Declaring `Service:home/thing` still works, because a declaration is never refused.

## Aliases do not rewrite anything

An alias says two names are one kind.
The vocabulary counts them together, so `service` reports the count of both, and `svc` does not appear as its own kind.

It does not rewrite refs.
An entity declared as `svc:home/thing` keeps that ref, because a ref is permanent ([ADR-0007](../adr/0007-entity-schema.md)) and Dusk never re-derives what a source already normalized ([ADR-0018](../adr/0018-normalization-at-the-edge.md)).
The alias makes the catalog read as one kind; renaming the entity is a separate decision, and a manual one.

## `.dusk/` holds two kinds of file

`.dusk/` is read whenever it exists, with no `include` ([ADR-0031](../adr/0031-notes-are-files.md)), and most of what is in it is catalog content: notes.

Two paths in it are Dusk's own configuration rather than catalog content, and a reconcile skips them:

- `.dusk/home.md`, the portal page ([ADR-0013](../adr/0013-layout-and-pages.md))
- `.dusk/kinds.md`, the vocabulary

They are named once, in `catalogfs`, because two owners of that rule is how they come to disagree.
A note cannot live at either path.
