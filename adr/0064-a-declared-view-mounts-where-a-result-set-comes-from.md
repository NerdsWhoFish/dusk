# 64. A declared view mounts where a result set comes from

Date: 2026-08-16

## Status

Accepted. Amends [ADR-0020](0020-plugin-ui.md), which is amended in place to say where each tier can mount.

## Context and Problem Statement

[ADR-0020](0020-plugin-ui.md) makes the declared view spec tier 1: the plugin describes what to show, Dusk's own React renders it, no JavaScript crosses and therefore no trust decision is asked of anybody.
It is the default, and the ADR is explicit that the zero-trust tier is the one that has to be easy.

A contribution names a slot, so a view about the plugin rather than about one entity has somewhere to go: the plugin's own page in the admin UI.
Creating a thing the catalog has never seen has no entity to mount against, which is what that slot exists for.

**A declared view in that slot silently renders nothing.**
The plugin page renders each contribution with no entities, and a declared view over no entities falls through to its own `empty` text.
An element does not notice, because it fetches its own data.
A spec has nothing to render and no way to say so, so an author following the documentation to the default tier gets a blank panel and no error to search for.

Two things found while reproducing it made the question different from the one recorded.

**The declared tier did not work in a page's `view` block either.**
Mounting resolved a contribution to an element tag and an asset URL and dropped the spec, so a page could only ever mount tier 2.
That matters because the block is the one surface where a page already supplies the result set, resolved server side per [ADR-0035](0035-blocks-resolve-server-side.md), and it is what "the way a homepage `view` block resolves a query" was supposed to point at.

**No real plugin's contribution was offered to a page at all.**
Mounting asked the manager for the views applying to every kind as a way of saying "do not narrow by kind", and those are not the same question.
Every contribution a shipped plugin makes names the kind it is about, so a `view` block naming a plugin answered that it contributes no such view.

## Considered Options

1. **The plugin page resolves entities for the slot**, so the declared tier works there.
2. **`conformance.ValidateDescribe` rejects a spec in that slot**, so it fails in the plugin's own tests rather than silently in Dusk.
3. **Both**: mount the declared tier wherever a result set already exists, and refuse it where none can.

## Decision Outcome

Chosen: **option 3**.

A declared view is a rendering of a result set, so something has to supply one.
There are three places a view mounts and each answers that differently:

| Where | What supplies the result set | Declared tier |
| --- | --- | --- |
| An entity page | The entity | Works, and always did |
| A page's `view` block | The block's `ref` or `query`, resolved server side ([ADR-0035](0035-blocks-resolve-server-side.md)) | Now works |
| The plugin's own page | Nothing | Refused |

### Why the plugin page does not resolve entities

Option 1 has to answer *which* entities, and every answer is Dusk choosing the question the view asks.

The most defensible set is what that plugin observed, narrowed by kind, and it needs two things the contract does not have.
It needs `applies_to_kinds` to mean something new in that slot, where conformance currently refuses it on the grounds that the slot mounts no entity.
And it needs a bound: a registry plugin observing 1385 repositories, or a network plugin observing 253 devices, is a table with no limit in a vocabulary that has no word for one.

A question and a bound are a query, and [ADR-0013](0013-layout-and-pages.md) already put the query on the page, where an operator writes it and a diff of it says what changed about the meaning of the page.
[ADR-0035](0035-blocks-resolve-server-side.md) then kept exactly one implementation of what a block's query means.
Putting a second query surface into `Describe` gives one concept two owners, and [ADR-0020](0020-plugin-ui.md) names this as its own worst consequence: the view spec is a vocabulary, and it will be under constant pressure to become a layout language.

The slot's stated purpose points the same way.
It exists for the case with no entity at all, which is somebody creating something, which is interactive, which is tier 2.

### Why refusing it costs less than it looks

The objection to option 2 is that it forces an author who wants anything on their own page into tier 2, and so into asking an operator for a trust decision the operator is badly equipped to make.

That objection was strong while the `view` block was broken, and it is mostly answered by fixing it.
An author who wants a zero-trust view of their data now has one: an operator writes four lines of frontmatter naming the plugin and the query, and the plugin decides only how the answer looks.
The page asks the question, which is the split [ADR-0013](0013-layout-and-pages.md) chose.

What is genuinely lost is a view on the plugin's own admin page with no page declaration anywhere.
That is a real cost and it is the price of not inventing a query on the plugin's behalf.

### Dusk says so as well as the plugin's tests

`conformance.ValidateDescribe` is a test a plugin runs, not a gate at install, so a plugin can still describe a spec in that slot and be installed.

The contribution is therefore refused where it is read: the spec is dropped so nothing downstream can draw it, and the reason takes its place on the page.
This is the anomalies rule in `docs/philosophy.md`.
A view that was never going to work must not be indistinguishable from one whose answer happens to be empty, and the operator reading it can act on it, because declaring a `view` block is something they can do themselves.

### A contribution is named by its element tag, or by its title

A page selects between a plugin's contributions with `element:`, which a declared view has none of.

`element:` therefore also matches a contribution's title, which conformance requires of every contribution and is the only name both tiers have.
The ambiguity message lists each candidate by whichever name selects it, because listing a name that selects nothing is worse than listing none.

## Consequences

### Good

- The tier that needs no trust decision is usable on a page, which is what [ADR-0020](0020-plugin-ui.md) says the default tier is for. It could not be used on one before.
- A plugin's own presentation of its own data is now available over an operator's own question, with no JavaScript in the page and nothing to approve.
- A `view` block finds the contributions a plugin actually makes, rather than only the ones that apply to every kind, which no shipped plugin declares.
- One accessor answers "what does this plugin contribute", and each surface narrows it for itself. There were two, and one of them was dead.
- A contribution that cannot render says why, in the place it would have rendered.

### Bad

- **A plugin's own page can hold only what the plugin draws**, so a summary of its own data there means shipping JavaScript and asking for tier 2. Some plugins will ship an element for something that should not have needed one.
- The refusal is enforced in two places for one rule: `conformance` in the plugin and the host when it reads a description. That is deliberate, because one is a test and the other is a running deployment, and it is still two.
- `element:` naming a title is a field doing two jobs. A single `view:` selector would read better and would break every page already using `element:`.
- `internal/page` now imports `internal/plugin` for the spec type. The alternative was a second declaration of the same shape, which is the drift `docs/packages.md` exists to prevent, but the dependency runs the less obvious way.
- Nothing stops a page mounting a declared view whose fields name attributes the block's query never returns. The result is a table of "not set", which is honest and not helpful.

### Rejected because

- **Resolving entities for the plugin slot** was rejected because choosing the set means choosing the question, and a question with a bound is a query. That belongs on a page, where an operator writes it and it is reviewed as a diff, rather than in `Describe`, where it would be a second query language owned by the plugin contract.
- **Only rejecting the spec**, with the `view` block left as it was, was rejected because it really would close the door on the default tier for anything not about one entity. The refusal is only reasonable because there is somewhere else to go.

## What the SDK has to change

`conformance.ValidateDescribe` lives in `NerdsWhoFish/dusk-plugin-sdk`.
`validateUI` gains one rule: a contribution whose slot is `UI_SLOT_PLUGIN` and which sets `spec` is a violation, because a declared view draws a result set and that page has none.

The existing rule refusing `applies_to_kinds` in the plugin slot stays, and its reasoning is now the same reasoning: that slot mounts nothing to filter and nothing to render.
`UIContribution.spec`'s comment says a spec is set or an element is, never both; it should also say a spec cannot be set in the plugin slot.
