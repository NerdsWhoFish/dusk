# 74. A result is whole in whichever half a client reads

Date: 2026-08-18

## Status

Accepted. Amends [ADR-0071](0071-mcp-results-are-dual-purpose-and-bounded.md).

## Context and Problem Statement

[ADR-0071](0071-mcp-results-are-dual-purpose-and-bounded.md) decided that every tool result carries two representations, and named one of them primary:

> Human-readable Markdown remains the primary content of every tool result.
> Successful results also carry compact structured content for clients that can consume it.

"Primary" was an assumption about clients, and a client falsified it.

Claude Code renders `structuredContent` and discards the content block entirely.
That is a defensible reading of the protocol: a tool that publishes an output schema is announcing that its answer has a machine-readable shape, and every tool here publishes one through `resultTool`.
So the half ADR-0071 called supplementary is, for that client, the whole answer.

For most tools this cost nothing visible, because the structured half carries the payload: `search` puts its hits in `results`, `note` its notes in `notes`, `get` the entity, its relations and its notes.
`dusk_context` is the exception, and it is the worst possible one.
Its structured half was three summary fields: the repository, what that repository declares, and a count of everything.
Its prose was the entire product: the pinned notes, the inventory, the note-id shape, the sentence about what absence means.

The observed failure: an operator whose catalog held 117 notes, three of them pinned, called `dusk_context` at the start of every session and received

```json
{"status":"ok","data":{"declared":["repository:stout/home"],"entity_count":2032,"repository":"…/home"}}
```

The three pinned notes were selected, ranked, rendered and thrown away by the client, and the call reported `ok`.
That operator's own agent instructions describe pinned notes as the thing to read before starting work, so the failure was silent on both ends: nothing said the notes were missing, and nothing said they had ever existed.

Diagnosing it from outside took a raw `curl` against the endpoint, because from inside a session the two hypotheses ("nothing is pinned" and "the pins are not arriving") produce identical evidence.

Two things made this survive review.
The test helper reads only `result.Content`, so the entire suite exercises the half that client discards.
And `success()` takes the body and the data as unrelated arguments, so nothing anywhere states that they must answer the same question.

## Considered Options

1. **Drop the output schema from `dusk_context`**, leaving prose as the only representation.
2. **Repeat the rendered body inside the structured half**, so both halves carry the answer.
3. **Give `dusk_context` a fully structured schema** (pinned notes, inventory and instructions as typed fields) and render the prose from it.
4. **Leave it, and document that clients must read the content block.**

## Decision Outcome

Chosen: **option 2**, narrowed by a rule rather than applied everywhere.

**Where the prose is the answer, the structured half repeats it.**
`dusk_context` returns its rendered body as `data.context`, byte-identical to the content block.
Where the structured half already carries the answer in typed form, nothing is repeated, because `get` would then answer twice at 70,000 characters a side.

ADR-0071's "Markdown is primary" is amended to a weaker and truer claim: **Markdown is the readable form, and neither half may be the only one carrying the answer.**
Which half a client reads is not something this repository gets to decide, and a decision that depends on client behaviour nobody controls is a guess.

Option 1 is the smallest change and was rejected for what it gives up.
A client reading structured content would get nothing at all rather than an incomplete answer, which trades a silent partial failure for a silent total one, and it forfeits the machine-readable `declared` and `entity_count` that a hook or a dashboard has a real use for.

Option 3 is the correct end state and is deliberately not done here.
The prose is not a serialization of a data structure; it is the product of a byte budget spent across competing sections ([ADR-0050](0050-what-the-context-budget-buys-first.md)), so "the typed fields" and "the rendered answer" are different things and a client rendering the former would be re-deciding the allocation without the budget.
Doing it properly means deciding what a *client* should be allowed to re-render, which is a larger question than the bug.

Option 4 was rejected because the failure is silent.
A contract a client can violate while still reporting `ok` is not enforced by documenting it.

## Consequences

### Good

- The context reaches the session on the client the operator actually uses, which is the whole point of the tool.
- The fix is one field and cannot regress the prose, because the two halves are the same bytes by construction.
- The rule generalizes: any future tool whose answer is prose knows what it owes the structured half.
- A test now reads the structured half, so the suite covers both representations of the tool where it matters most.

### Bad

- `dusk_context` carries its body twice, up to double [ADR-0014](0014-agent-context-injection.md)'s budget on the wire. That is a real cost paid by every client, including the ones that needed only one copy.
- Two fields must stay identical, enforced by a test rather than by a type.
- It fixes the instance rather than the class. Every other tool is fine *today* because its structured half happens to be complete, and nothing stops a new tool from putting the answer only in prose.
- The suite still reads the content block almost everywhere, so this class of bug is only guarded at the one site that hit it.

### Rejected because

- **Dropping the output schema** was rejected because it converts a partial silent failure into a total one for the same client, and discards structured fields that have real consumers.
- **A fully structured context** was rejected as the right answer to a larger question: the prose is a budget allocation, not a serialization, and letting a client re-render it moves [ADR-0050](0050-what-the-context-budget-buys-first.md)'s decision to the client.
- **Documenting the contract** was rejected because nothing enforces it and the violation reports success.
