# 61. A token names the write it authorizes

Date: 2026-08-15

## Status

Accepted. Extends [ADR-0009](0009-proof-tokens.md), and applies its second amendment to the offer rather than to the refusal.

## Context and Problem Statement

[ADR-0009](0009-proof-tokens.md) requires every read to hand back a proof token unasked, because read-before-write is an unusual contract and an agent that has to discover it will flail.
One sentence carried every one of them:

```text
Proof token `4QK7…`. Pass it to `declare` or `note` to write any of the above.
```

That sentence is true for a search that found something and for a `get`.
It is false everywhere else, and driving the surface as a fresh agent found the falsehoods in a single session.

**On a `page` read it names two tools that refuse the token.**
The homepage is a file at a fixed path, and `page` is the only call that writes it.
An agent told to pass a page token to `declare` passes a file path where an entity ref belongs, is refused, and has been sent there by the answer it is trying to follow.

**On an empty search, "any of the above" refers to nothing at all.**
The token is real and matters, because an empty search is exactly what authorizes creating ([ADR-0009](0009-proof-tokens.md)), but the sentence describes a list that is not there and never mentions the thing the token is for.

**On a note read it names `declare`, which cannot write a note.**
A note's id is its path, and `declare` takes an entity ref.

**On a `neighbors` walk it offers "any of the above" over a page of refs it does not cover.**
That token covers the one entity the walk started from; the other refs were named, never read.

**On a note read that matched nothing it offers a token that covers nothing.**
Writing a new note needs no token at all, so there is no call that can spend it.

`kinds` was the exception, and had been all along: it writes its own line, "Pass it with `mint` to add a kind", because nothing generic could have said it.
That is the shape the rest of the surface needed, and one tool having it privately is why nobody noticed the others were wrong.

This is the same defect [ADR-0009](0009-proof-tokens.md)'s second amendment fixed on the refusal path, arrived at from the other side.
There, every rejection rendered `get(<ref>)` because the code derived the call from the ref; the fix was to make the write path supply the call as a second fact.
Here every read renders `declare` or `note` because one helper derives the offer from nothing at all.

## Considered Options

1. **Leave one sentence, and make it vaguer.** "Pass it to whichever write tool applies."
2. **Derive the offer from the token's `proof.Origin`**, which already records which read issued it.
3. **The read names the write**, the way `kinds` already did.

## Decision Outcome

Chosen: **option 3**.

**The read that issues a token says what the token can be spent on.**
`issue` renders the token and the sentence the caller supplies, and every read supplies one:

| Read | What it offers |
| --- | --- |
| `search` with hits | `declare` or `note` for what it returned, and that it also authorizes creating what it did not find |
| `search` with none | `declare`, to create what the search did not find |
| `get` | `declare` for the entity, and `note` for one of the notes that came with it |
| `neighbors` | `declare` for the one ref the walk read, named |
| `note` with hits | `note`, with the id of one of them |
| `note` with none | nothing, and the answer says a new note needs no token |
| `page` | `page`, to declare the first one or to replace the whole of it |
| `kinds` | `mint`, unchanged |

Whether `note` is named at all still depends on the deployment, because a Dusk with nowhere to put a note cannot take one, and offering a write that always refuses is the defect one level down.
The tool itself stays registered either way, since reading what is written down is a read.

### Deriving it from the origin was the close call

Option 2 looks like the DRY answer, and `Origin` even carries the right information for four of the six cases.
It was rejected for the reason [ADR-0009](0009-proof-tokens.md)'s amendment gives about the fix a rejection names: the offer is not a function of the token.

Two reads with the same origin want different sentences.
`search` with hits and `search` with none differ by what is on the page above the line, not by which read ran.
`get` names `note` only when notes came back with the entity, and `note` names nothing at all when the read matched nothing.
A table keyed on origin would have to be consulted with those facts anyway, at which point it is the caller's sentence with an indirection in front of it.

The deeper reason is that a derivation cannot be wrong in a way anybody sees.
The sentence that shipped was derived, and it was wrong on four reads for as long as it existed, because nothing about the code said which tool spends a page token.
A sentence written where the read is written is read by whoever changes that read.

### An empty note read issues nothing

The other reads keep issuing a token whether or not they matched, because an absence is evidence: [ADR-0009](0009-proof-tokens.md) gates creating on the read that could have found the thing and did not.

A note read is the exception, and only because notes are the one thing Dusk writes that needs no token to create ([ADR-0053](0053-note-dedup.md): the path is the body's hash, so a create cannot overwrite).
There is nothing for that token to authorize.
The answer says so, so an agent that has learned every write needs a token does not conclude it cannot write.

## Consequences

### Good

- Every offer is a call the agent can actually make, which is the property [ADR-0009](0009-proof-tokens.md) asks of a rejection and never asked of the offer.
- The `page` write path becomes reachable by following the answer rather than by knowing the tool. A first homepage was already gated on `page`'s own token; nothing said so.
- A `neighbors` token stops advertising coverage it does not have, which was the most misleading of the six because it was the least obviously wrong.
- One place renders the token line, so `kinds` stops being a private copy of the format.
- The sentence sits beside the read, so changing what a read returns puts the offer under the same eye.

### Bad

- **Eight sentences where there was one**, and nothing checks that they stay true when the read changes. The test names each of them, which is the whole of the enforcement.
- They will drift in tone, because prose written in eight places is written eight ways.
- Building the offer at the call site means it is built before `issue` can decide there is nothing to offer, so a helper that reads the writer has to survive a read-only deployment that has none. That is a nil check somebody will forget; the panic it caused is why the accessor holds it rather than each caller.
- An agent built against the old sentence, matching on "write any of the above", stops matching on four reads.

### Rejected because

- **One vaguer sentence** was rejected because "whichever write tool applies" is the question the agent is asking. Vagueness makes the sentence unfalsifiable rather than true.
- **Deriving the offer from the origin** was rejected because the offer is not a function of the read: it depends on what the read returned, and a wrong derivation is invisible, which is how the one being replaced survived.
