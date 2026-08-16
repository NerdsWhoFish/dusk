# 59. What a list may not leave unsaid

Date: 2026-08-15

## Status

Accepted. Extends [ADR-0010](0010-mcp-surface.md) and applies [ADR-0050](0050-what-the-context-budget-buys-first.md)'s rule to the rest of the surface.

## Context and Problem Statement

[`docs/mcp.md`](../docs/mcp.md) states the house rule for every answer the surface gives:

> **Absence is explained, never silent.**
> Searching for something nobody has declared says so, and points at `changes`.
> An agent that cannot tell "not in the catalog" from "the catalog is empty" will invent the difference.

`dusk_context` honours it, because [ADR-0050](0050-what-the-context-budget-buys-first.md) made it honour it.
Nothing else did.
Driving the surface against a real catalog found four failures, in ascending order of how much they cost.

**A `get` on a heavily annotated entity was 78,720 characters.**
Every note attached to it printed whole, with no ceiling and no way to ask for less.
[ADR-0010](0010-mcp-surface.md) made `get` fat on purpose, and "fat" was implemented as "unbounded", which are not the same thing.

**A list of refs was 22 refs and nothing else.**
An entity related to twenty-two others answers with twenty-two identifiers, so "which of these is worth opening" costs twenty-two more calls.

**A note list cut at ten with nothing saying so.**
`note(kind: "runbook")` returned ten of thirty-five runbooks, whole, in id order, and said nothing about the other twenty-five.
An agent reads 67,836 characters, believes it has seen the runbooks, and has seen 28% of them.
The one that was actually wanted sorted past the cut and was unreachable.

**And a kind-filtered search claimed nothing matched while matches sat just past the window.**
`search` took a raw page of twenty-five hits and *then* dropped the ones of the wrong kind, so a search whose first twenty-five hits were all services answered "Nothing in the catalog matches" for a host that matches.

The last one is the reason this is an ADR rather than four commits.
Dusk teaches agents, in its own tool instructions, that "an entity being absent means nobody has written it down".
A fabricated absence is not a missing answer, it is a wrong one, and it poisons exactly the reasoning the product asks agents to rely on.
The other three are the same failure with the volume turned down: an answer that is smaller than the truth and does not say so.

## Considered Options

For the filter:

1. **Filter the answer, and fetch more rows to compensate.** Take 200, filter to 25.
2. **Filter the answer, and say the page was cut.** Keep the pass, report that it happened.
3. **Push the filter into the query.**

For saying how much was not shown:

1. **Say nothing**, which is what the surface did.
2. **Fetch one row past the limit** and say "more exist".
3. **Count what matched**, and say "showing N of M".

For an answer too large to print whole:

1. **Leave it unbounded.** `get` is fat by decision.
2. **Cut it at a row count.**
3. **Bound it in bytes, and name what did not fit**, as [ADR-0050](0050-what-the-context-budget-buys-first.md) does for the context.
4. **Add a mode that returns identifiers only**, and leave the default unbounded.

## Decision Outcome

Chosen: **push the filter into the query**, **count what matched**, and **bound in bytes while naming what did not fit**, with a titles-only mode as an argument rather than as the answer to the size problem.

Four rules, which together are one rule: **a list says what it holds, what it is not showing, and never reports an absence it has not verified.**

### A filter narrows the query, never the answer

`kind` becomes part of the search query rather than a pass over the rows it returned.
The same fix lands in all three places that had the bug: the MCP tool, the homepage's entity blocks, and the web API's search endpoint.

Option 1, taking a larger raw page, is the fix that looks equivalent and is not.
It moves the failure rather than removing it: with 201 services and one host, "fetch 200 and filter" tells the same lie, and the number that has to be raised is unknowable in advance because it depends on the catalog rather than on the query.
A bug that only appears above some size is worse than one that appears always, because it passes the test somebody writes for it.

Option 2 is honest and still wrong, because "your page was cut" is not an answer to "is there a host called this".

Visibility is deliberately **not** moved with it.
[ADR-0051](0051-a-count-is-of-what-the-viewer-can-see.md) settled that a list is filtered where it is rendered and a count where it is computed, so an unrestricted viewer pays nothing.
That reasoning is about a predicate that costs something for one class of caller; a kind is free and applies to everyone.

### Every list says how many matched

`search` and `note` report "N of M".

For search the count is free: SQLite computes it in the same statement with `COUNT(*) OVER ()`, over the rows that passed the `WHERE` and before `LIMIT` applies.
FTS5 has already enumerated every match in order to rank it, so nothing new is read.

For notes it is a second query, `CountNotes`, sharing one predicate builder with the read so the two cannot disagree about what the filter means.
Returning the total from `Notes` instead was rejected because three of its four callers do not want it: the homepage's recent-notes block, a page's note block, and the context sections all ask for a fixed number and would carry an unused int.
Two statements against a local SQLite file is not a cost worth a worse signature.

Option 2, fetching one row past the limit, is cheaper still and buys "there are more" rather than "there are thirty-five".
It was rejected because the number is what makes the answer actionable: an agent told "10 of 35" knows to raise the limit or narrow the filter, and an agent told "there are more" knows only that it was not told something.

### A list too large to print whole names its tail

Notes are printed whole while they fit a byte budget, and the rest print as their kind, their id and their opening line, which is [ADR-0050](0050-what-the-context-budget-buys-first.md)'s degradation form and [ADR-0031](0031-notes-are-files.md)'s reason for it: a note has no title, so its opening line is the closest thing it has to one.
The line above says how many arrived whole and how many were named.

This is what bounds `get`, and it is a change to [ADR-0010](0010-mcp-surface.md), which is amended to say so.
"Deliberately fat" was about *what* `get` answers, not about how much of it arrives at once: the argument was that an agent wants the description, the attributes, the relations, the notes and the actions in one call rather than five.
All of that still arrives.
What changes is that the twelfth runbook attached to a service arrives as a line naming it instead of as four thousand characters, which is strictly more useful than the alternative that was actually on offer, since an agent that spends its context on notes eleven through thirty-five has less room for the answer it came for.

Option 2, cutting at a row count, is what the note tool already did and is the failure being fixed.
Option 4, an identifiers-only mode with the default left unbounded, puts the fix behind an argument an agent has to know to pass, and the agent that most needs it is the one that has never called `get` before.
The mode is still worth having and is kept as `titles`, for an agent that wants to know what is attached without reading any of it; it is just not the answer to the size problem.

The note read's own default limit rises to a number above what the budget could ever name, for [ADR-0050](0050-what-the-context-budget-buys-first.md)'s reason: the byte budget should decide what is shown, rather than a row limit nobody sees hit.

### A ref in a list carries the name of what it points at

A relation renders with the title of the entity at the other end, resolved for every relation in one query.

This is [ADR-0010](0010-mcp-surface.md)'s first rule read the other way.
"Every read returns refs that feed back into `get`" makes a ref the input to the next call, and that is worth nothing if choosing which ref to pass costs a call per candidate.

The **status** of what a ref points at is deliberately left out, and belongs to whichever plugin declared it.
"In Progress" is a board's concept, not the catalog's; Dusk has no notion of an entity's status and would have to guess that the attribute spelled `status` is the one worth printing, which is Dusk inventing a schema for data a plugin already normalized ([ADR-0018](0018-normalization-at-the-edge.md)).
A plugin that wants a status visible from the thing that lists it has three levers that already exist: put it in the entity's title or description, where search will find it; declare a view block ([ADR-0013](0013-layout-and-pages.md)); or offer it as an action ([ADR-0041](0041-plugins-reach-agents-as-actions.md)).

## Consequences

### Good

- The one answer that must never be invented cannot be invented by this path any more. A kind-filtered search that says nothing matches has looked at every match.
- One rule covers four surfaces and is stated once: a list says what it holds and what it is not showing.
- `get` is bounded, so the worst case for one call is a number rather than however much somebody has written down. The number is a constant, not a setting, for [ADR-0050](0050-what-the-context-budget-buys-first.md)'s reason: a limit that moves whenever it binds is advice.
- Nothing is lost to the bound. A note that does not fit is still named and still reachable, so the agent knows what it did not read.
- The kind fix lands in the index, so all three surfaces that had the bug are fixed by one change rather than three, which is the "one concept, one owner" rule paying out.
- Choosing which of twenty-two related things to open no longer costs twenty-two calls.

### Bad

- **The byte budget is a second arbitrary number**, beside [ADR-0014](0014-agent-context-injection.md)'s. It was picked to leave an ordinary entity's notes whole and cut only the pathological case, and it will be wrong for somebody's catalog with no way to say so.
- An agent that relied on `get` returning every note whole now has to make a second call for the tail. That is a behaviour change to a surface agents may have been built against, and [ADR-0010](0010-mcp-surface.md) predicted it would be annoying to reverse once agents depend on the shape.
- The note count is a second query, so a note list is two statements where it was one.
- Resolving relation titles is another query on every `get`, and an entity with a thousand relations pays for a thousand titles.
- **Relations are still unbounded.** Only the notes half of `get` is budgeted, so an entity with a thousand relations still answers with a thousand lines, now longer ones. Recorded in [`docs/status.md`](../docs/status.md) rather than fixed, because nothing has hit it and a second budget with no case behind it is the speculative machinery this repository keeps rejecting.
- Two counts in one answer, "showing N of M" and "X whole, Y named", read as arithmetic and somebody will misread one for the other.
- `COUNT(*) OVER ()` prevents SQLite from stopping early on the limit. For FTS5 ordered by rank it never could, so the cost is zero there and would not be for a query that could.

### Rejected because

- **Fetching a larger raw page and filtering it** was rejected because it does not fix the bug, it raises the catalog size at which the bug appears, and that size is not knowable from the query.
- **Reporting that the page was cut, without pushing the filter down**, was rejected because it answers a different question than the one asked.
- **Fetching one row past the limit** was rejected because "there are more" is not actionable and the exact count is nearly free.
- **Returning the total from `Notes`** was rejected because most of its callers do not want it and would carry it unused.
- **Leaving `get` unbounded** was rejected because "fat" was a decision about completeness, not about volume, and 78,720 characters in one tool result is a cost the caller cannot see coming or opt out of.
- **Cutting the notes at a row count** was rejected as the exact failure being fixed one level up.
- **A titles-only mode as the whole answer** was rejected because it is opt-in, and the agent that needs it most is the one that does not know it exists.
- **Printing a related entity's status** was rejected as Dusk re-deriving what a plugin normalized. The plugin owns the concept and already has three ways to surface it.

## Amendments

None yet.
