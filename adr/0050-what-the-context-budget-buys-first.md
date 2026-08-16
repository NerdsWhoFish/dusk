# 50. What the context budget buys first

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

[ADR-0014](0014-agent-context-injection.md) gave `dusk_context` a hard ceiling and said what happens when it is exceeded: "Exceeding the budget truncates and warns."
It also named the failure that ceiling exists for, which is that "people will pin too much, because pinning is free to them and the cost lands on every future session".

Pinning did not cost a session anything, because nothing pinned ever reached one.
The entity schema documents `pinned` as marking "candidates for injection into agent context at boot", `ContextBudget` reasons explicitly about what pinning costs, and `dusk_context` rendered owned entities, an inventory and a drift summary.
No notes at all.
Pinning only reordered a list.

That is the gap between "the operator wrote down a gotcha" and "the agent knew it before it started", and the notes waiting on the other side of it are not hypothetical.
Three real ones, from one afternoon of cataloguing:

- a service in the cluster is built from a source repository that no longer exists, so it cannot be changed
- no Kustomization sets a `decryption` block, so "the secret is in git" and "the secret is in the cluster" are independent facts
- one application's namespace still carries its former name, deliberately, because renaming it would mean moving encrypted data

An agent starting work in one of those repositories should not have to think to ask.
None of them is discoverable by searching, because nobody searches for what they were never told exists.

Putting notes in the answer is four lines.
Deciding what they displace is the decision, because the budget is already spent.
Whatever is added has to come out of the inventory, and the one thing that must not happen is the code silently rendering less: `truncate` in that file already says a silently shortened context degrades every answer with nothing to connect the degradation to.

## Considered Options

1. **Append the notes and let `truncate` do the work.** One line. The existing cut already warns, so it is not silent.
2. **Give each section a fixed share of the budget.** Notes get a quarter, the inventory a half, and so on.
3. **Spend the budget in priority order, and print in reading order.** What is worth most is paid first, whatever position it occupies in the answer.
4. **Raise the ceiling.** 8000 bytes is arbitrary, and a larger one fits both.
5. **Inject note titles only, never bodies.** Cheap per note, so many more fit.

## Decision Outcome

Chosen: **option 3**, with a share cap so no section can starve the ones after it.

The answer is assembled from four sections. They are written in one order and paid for in another:

| Read in this order | Paid in this order |
| --- | --- |
| Pinned, about this repository | Pinned, about this repository |
| What this repository declares | Pinned, across the estate |
| Pinned, across the estate | What this repository declares |
| What this operator has | What this operator has |

The rule behind that ranking is that **written knowledge outranks enumerable fact**.
A ref left out of the answer is one `search` away and the agent has been told so in the same breath.
A gotcha left out is reachable by nothing.

### Relevance orders the pinned set; it does not widen it

`dusk_context` takes the directory the agent is working in, so it knows which repository the session is about.
A pinned note attached to something that repository declares is worth more than a pinned note about something else, and that is the only ranking axis this decision adds.

It does not lower the bar for entry.
An unpinned note about this repository still does not appear, because pinning is the operator saying a note is worth every future session, and relevance decides the order among the notes that already earned that.
Widening past pinning is note ranking by kind, so that a gotcha outranks a todo without being pinned by hand.
That is a separate decision and it feeds this one as a third key when it lands.

### Nothing is dropped without being named

Every section carries an overflow line, and the room for that line is reserved before anything is spent, so even a section that fits nothing can say it exists.

A note that does not fit whole is printed as its kind, its id and its opening line, which is the closest thing a note has to a title: [ADR-0031](0031-notes-are-files.md) makes the id a path and gives a note no title field.
An agent that has read "**gotcha** · `.dusk/sops.md`: nothing decrypts a sealed secret on the way in (not shown)" knows there is something to ask for.
A kind whose entities do not fit prints its count instead of its names.
Below that, a section says how many entries it left out and which tool answers them.

`truncate` stays as a backstop, because a section added later that forgets to declare an overflow line should produce a visible failure rather than a quiet one.
Under this allocation it does not fire.

### A share cap, because one section must not silence the rest

No section takes more than half the spendable budget until every other section has had a turn, and a second pass hands on whatever the capped sections did not want.

Without the cap, forty pinned notes erase the inventory completely, and the inventory is the other half of what ADR-0014 promises.
Without the second pass, a cap would waste budget in the ordinary case where only one section has anything to say.

## Consequences

### Good

- The gap between writing something down and an agent knowing it closes, which is the whole argument for notes being first class.
- The order in which content is lost is now a decision rather than a side effect of the order somebody happened to append it in. The closing sentence explaining what absence means used to be the first casualty of a large catalog, because it was written last.
- A large kind degrades to a count rather than eating the budget, so one plugin emitting nine hundred airports no longer crowds out everything declared by hand.
- The reserve makes "nothing is dropped silently" a property of the assembly rather than a promise each section has to keep, and a test can assert it by arithmetic: notes named plus notes reported missing equals notes pinned.
- The two orders are two slices of the same sections, so changing what matters is changing a line rather than moving code.

### Bad

- The ranking is a policy decision and it will be wrong for somebody. An operator who wants the whole inventory and none of the notes cannot say so, and there is no setting.
- `dusk_context` is now the only place in Dusk that judges catalog content against other catalog content. Everywhere else the operator declares what they want to see, which [ADR-0013](0013-layout-and-pages.md) made deliberate. This is the exception, and a second one would be a pattern worth worrying about.
- Saying what was left out costs bytes that could have carried content, so a badly over-budget answer spends several lines on apologies.
- Degradation within a section is depth first: the highest ranked notes arrive whole and the tail becomes one-liners. Breadth first, printing every title and then filling with bodies, would suit an agent that wants to know what exists more than what it says. It was not built, and switching is a change to one loop.
- Relevance is computed from what a repository declares, so a repository that declares nothing gets no local notes however much has been written about it. Most repositories declare nothing until somebody adopts them.
- A note attached to nothing is estate wide by construction, so an idea pinned while working in one repository reads as being about the whole estate.
- The share cap is a second number with no principle behind it beyond "not all of it".

### Rejected because

- **Option 1** was rejected because it inverts the ranking exactly where it matters. Appending notes makes them the first thing lost in the only catalogs large enough to need them, and it is what the current code does to the closing guidance. "It warns" is true and not sufficient: a warning that content was lost is not the same as choosing which content.
- **Option 2** was rejected because ratios describe a catalog nobody has. A catalog with three pinned notes and four hundred entities would get the same split as one with three hundred pinned notes and four entities, and both would be wrong. The share cap kept here is not this option: it binds only until every section has had a turn, and unspent budget flows on rather than being held for a section that does not want it.
- **Option 4** was rejected because the ceiling is the point. Every session pays the context cost whether or not it touches Dusk, and a limit that moves whenever it binds is advice. The ranking has to be right at any ceiling, and raising it only delays finding out that it is wrong.
- **Option 5** was rejected because a note's whole value is its body, and a note has no title to inject: its id is a path. An agent handed a list of paths has been told to make another call, and for a gotcha that means it will not read it. The title form is kept for exactly the case it is good for, which is a note that could not fit whole.

## Amendments

Amendment policy: [ADR-0028](0028-amending-adrs.md).

### 2026-08-15: a section is charged what it printed, and the share binds on the remainder

The allocation this ADR shipped does not survive the catalog it was written for, and [ADR-0057](0057-charged-for-what-was-printed.md) records what replaced it.

Two things here are corrected rather than extended.

The second pass is described as handing on "whatever the capped sections did not want", and it hands on what they did not **want** rather than what they did not **spend**.
A section holding notes too long to print whole wants their bodies and spends two title lines, so it holds most of the budget and prints almost none of it.
A section is now charged what it printed.

"No section takes more than half the spendable budget until every other section has had a turn" is not true of an answer with four sections, because two halves are the whole.
The share now binds on what is left when a section's turn comes.

[ADR-0057](0057-charged-for-what-was-printed.md) also settles what this ADR did not: the order kinds are listed in, and what an overflow line says about the entries it dropped.
The doctrine here is unchanged.
Priority order against reading order, written knowledge outranking enumerable fact, relevance ordering the pinned set without widening it, and nothing dropped without being named all stand.
