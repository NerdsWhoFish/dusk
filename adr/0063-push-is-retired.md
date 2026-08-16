# 63. `push` is retired, because there is no queue to flush and no session to report on

Date: 2026-08-16

## Status

Accepted. Retires the `push` tool from [ADR-0010](0010-mcp-surface.md), which is otherwise unchanged.

## Context and Problem Statement

[ADR-0010](0010-mcp-surface.md) named five write tools.
Four of them are built.
The fifth is `push()`, described there as "flush the session's queued commits", and it is the last unbuilt item on the write path.

It was one part of a mechanism rather than a feature of its own.
That ADR queued every write on a per-session branch in a local clone, and `push` was what turned the queue into either a fast-forward or a pull request, depending on the access mode.
Sessions that ended without pushing were to be flushed automatically, and abandoned branches swept, "because a queue nobody flushes is silent data loss".

**The mechanism it belonged to is gone, and it went deliberately.**
[ADR-0010](0010-mcp-surface.md)'s own 2026-08-11 amendment removed the queue from write mode: a commit is made through the GitHub API, straight to the default branch, one per call.
It said so plainly, and it also said what was left:

> `push()` therefore has nothing to flush in write mode and reports what already landed.
> It keeps its full meaning in proposal mode, where it is what opens the pull request.

Both halves of that sentence have since stopped being true, for reasons recorded elsewhere and never carried back here.

**Proposal mode is not pending, it is declined.**
The per-session branch and the pull request are on the Declined list in `docs/status.md`: the mode was granted `contents: read` and so could never commit, and since [ADR-0052](0052-a-write-that-cannot-land.md) a write in it returns the proposed diff exactly as read mode does.
There is nothing it offers that read mode does not.
So the half of `push` that kept "its full meaning" has no mode left to have meaning in.

**"Reports what already landed" cannot be built as described**, and it took writing it out to see why.
It needs two things Dusk does not have.

It needs a session.
The MCP surface authenticates with one shared bearer token and carries no per-caller identity, which is already recorded as the reason an event's actor is always `agent` and two agents holding one token are indistinguishable.
"The session's commits" is not a set Dusk can compute.

It needs a record of writes.
Events are a bounded in-memory buffer of action invocations ([ADR-0015](0015-plugin-actions-and-events.md)), durable events are declined, and a write is not an event at all.
Nothing anywhere holds "what this caller committed".

And even given both, it would answer worse than the calls it summarised.
[ADR-0010](0010-mcp-surface.md)'s third rule makes every write report where it landed, with the commit URL, precisely so an agent can hand a human a link rather than claiming success.
The caller already holds every link `push` would repeat.

## Considered Options

1. **Build it as the amendment describes**, reporting what already landed.
2. **Build it as a batching commit point**, holding writes and committing them together.
3. **Leave it unbuilt and undecided**, as a `[ ]` on the status list.
4. **Retire it.**

## Decision Outcome

Chosen: **option 4**.

`push` is not built, and the surface has eight tools rather than nine.

### A tool that can only answer "nothing to flush" is worse than no tool

[ADR-0010](0010-mcp-surface.md) makes the size of the tool list a product constraint rather than an implementation detail, because it is spent on every session before any work happens.
A tool that costs that and then explains it has nothing to do is a straight loss.

The codebase already applies this rule in the other direction: `note` is not offered at all in a deployment with nowhere to put a note, on the reasoning that a tool that always fails is worse than one that is absent.
`push` is the same case reached by a different route, and answering it differently would be two rules where there is one.

Option 3 is the status quo, and it is the one worth arguing against hardest, because it costs nothing today.
It loses because "later" and "abandoned" are indistinguishable from outside, which is the reason this repository requires anything deferred to leave a trace.
An unbuilt item on a checklist reads as work somebody will do.
This is not work somebody will do; it is a mechanism that was replaced, and saying so is the whole value of the record.

### Batching was the one real thing it could have bought, and it is not this

Option 2 is not what `push` meant, but it is the strongest thing it could have become.
[ADR-0010](0010-mcp-surface.md) lists "one commit per call produces a verbose history" as a bad consequence it accepted, and a call that closed a batch would pay it down.

It is rejected here because it re-takes a trade that was made deliberately.
The 2026-08-11 amendment gave up atomic multi-call sequences and got back "the whole queue lifecycle, including the automatic flush and the abandoned-branch sweep that this ADR itself described as guarding against silent data loss".
A batch is a queue with a different name, and it brings the same question back: what happens to a batch nobody closes.
An operator who chose write mode has already said they trust the agent, and a commit that simply lands is a better trade for them than a batch that has to be swept to be safe.

If verbose history becomes a real complaint, the answer is a decision about commit granularity with that evidence in front of it, not a tool that exists now in case it is wanted later.

### Proposal mode, if it is ever built, names its own call

Opening a pull request needs some call to open it.
That call belongs to the decision that builds proposal mode, and it should not inherit this name: `push` is a queue-flush verb, and the thing it would name is not a queue.

Retiring this leaves that decision free rather than constrained, which is the opposite of what an unbuilt placeholder does.

## Consequences

### Good

- The write path is finished rather than perpetually one item short, and `docs/status.md` stops carrying an item nobody was going to build.
- The tool list stays at eight, against a constraint [ADR-0010](0010-mcp-surface.md) set for itself.
- The reasoning is written down once. Without it, the next reader finds `push()` in an accepted ADR, does not find it in the surface, and re-derives all of this or, worse, builds it.

### Bad

- **[ADR-0010](0010-mcp-surface.md) now lists a tool that does not exist**, in a section a reader reaches before the amendments at the bottom. The amendment there points here, and that is the whole of the mitigation.
- **Nothing batches writes**, so a long session still produces a commit per call and a verbose history, which is a consequence [ADR-0010](0010-mcp-surface.md) accepted and this decision declines to pay down.
- **A multi-call sequence is still not atomic in write mode.** A failure partway leaves the earlier calls committed, and there is now no call that could ever have made it otherwise. That was already true after the 2026-08-11 amendment; retiring `push` removes the last thing that looked like it might fix it.
- An agent built against [ADR-0010](0010-mcp-surface.md)'s list will call `push` and be told no such tool exists. There is no deprecation path, because the tool never shipped.

### Rejected because

- **Building it to report what landed** was rejected because it needs a session identity the surface does not have and a record of writes nothing keeps, and because every write already returns its own commit URL, so the best version of it repeats what the caller is holding.
- **Building it as a batching commit point** was rejected because a batch is a queue, and the 2026-08-11 amendment gave up the queue on purpose to be rid of the flush-and-sweep lifecycle that a batch nobody closes brings straight back.
- **Leaving it unbuilt** was rejected because an item on a checklist reads as pending work, and this is replaced work. The difference between "not yet" and "no longer" is exactly what a decision record is for.
