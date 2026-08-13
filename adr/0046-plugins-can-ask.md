# 46. A plugin can ask the human a question, by returning it

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

An action takes its parameters up front and runs.
That is right for restarting a workload, where the agent already knows the target and there is nothing to gather.

It is wrong for anything whose input is a decision rather than a lookup.
The case that forced this was an ADR plugin: writing a decision record means supplying a problem statement, the options considered, the outcome and its consequences.
An agent invoking that action with parameters it made up produces a record whose rejected options nobody ever rejected.
That is worse than no record, because it looks like history.

The obvious answer is MCP elicitation, where a server asks the client for structured input mid tool call, and the client puts it to the person.
The go SDK supports it. The question is not whether it can be done but what shape it takes inside Dusk, because Dusk is a server and an action reaches it from three places:

- an agent over MCP, which may have a human attached
- the web UI, which has no MCP client at all
- a `Next` step in a composed chain, or a scheduled run, where nobody is attached to anything

[ADR-0041](0041-plugins-reach-agents-as-actions.md) makes one declaration serve every one of those surfaces.
A capability that only functions on one of them breaks that promise quietly, which is the worst way to break it.

## Considered Options

1. **Do not support it.** The calling agent gathers input and invokes with complete parameters. The agent is already a conversation with a human in it.
2. **A blocking callback mid `Invoke`.** Make `Invoke` a bidirectional stream so the plugin can ask and wait inside one call.
3. **Return the question and end the turn.** `InvokeResponse` carries an `Elicit`. Dusk asks whoever is attached, then invokes the same action again with the answer.
4. **A separate RPC for gathering, before invoking.** The plugin declares an interview, Dusk runs it, then invokes once with everything.

## Decision Outcome

Chosen: **option 3**.

`InvokeResponse.elicit` ends a turn and nothing else in that response is acted on: no chain runs and no refresh happens, because the action has not finished.
Dusk puts the question to the invoker and invokes again with `InvokeRequest.elicited`, carrying the answers, the plugin's own opaque token, and how it ended.

### Returning the question is what makes it degrade instead of hang

This is the property the decision turns on.

A blocking callback (option 2) has no answer for a surface with nobody attached.
It either hangs until a timeout or fails, and the plugin author cannot tell the difference between "nobody is there" and "the human is thinking".

Returning the question makes the absence of a human an ordinary answer.
When no elicitor is attached, Dusk replies `unsupported` and invokes again, and the plugin chooses: use a default, do the part it can, or refuse in its own words.
The same declaration therefore works from an agent, from the UI and from a schedule, and the difference is visible to the plugin rather than hidden from it.

This is [ADR-0015](0015-plugin-actions-and-events.md)'s "a missing link is loud", one level down.

### The browser answers too, by the same mechanism one level up

An MCP session is a live connection, so Dusk can put the question and wait inside the call.
An HTTP request cannot: nothing can hold a browser open while somebody reads a form.

Rather than build a second mechanism, the UI gets the plugin's own shape.
A request that sets `CanResume` receives the question on the `Outcome` instead of a result, the browser renders it from the same schema it already renders action parameters from, and answering invokes the same action again with `Elicited` set.
The plugin's token carries whatever it needs to continue, so Dusk holds nothing between the two requests and a half finished action cannot leak.

The recursion is the point: the plugin returns its question to Dusk, and Dusk returns it to the browser, for the same reason in both cases.

### It keeps one transport and one RPC shape

Option 2 needs a bidirectional stream. [ADR-0039](0039-one-plugin-transport.md) chose a socket precisely because every language handles it comfortably, and streaming gRPC is comfortable in far fewer.
Making the central RPC of the contract streaming would re-decide that by the back door, the same way [ADR-0044](0044-plugins-keep-the-socket-directory.md) refused a socketpair.

`Invoke` stays unary. A plugin that never asks is unchanged, and `buf breaking` confirms it: every field here is additive, and the five plugins written against the previous contract still build.

### It is symmetric with composition, which already exists

`InvokeResponse.then` already means "here is what should happen next, you run it".
`InvokeResponse.elicit` means "here is what I need, you get it".
Both are the plugin describing something for Dusk to do and then being called again.
A reader who understands one understands the other, and neither requires the plugin to hold a half finished call in memory: the token carries whatever it needs to resume.

### Why not leave it to the agent

Option 1 is what this repository would have done a week ago, and the argument for it is strong: the calling agent is already a conversation with the human, and it has context an elicitation form never will.

It loses on one point, and the point is the reason the ADR plugin exists.
The agent gathering input is the same agent that might invent it.
An elicitation is a guarantee that the *human* was asked, expressed by the plugin that owns the rules, rather than a hope that the agent bothered.
For a decision record, that guarantee is the product.

Option 1 remains the right answer for most actions, and nothing here obliges a plugin to ask.

### Bounded, because a question is a loop waiting to happen

An action may ask at most four times before Dusk stops it and fails the invocation.
A plugin still asking after four turns is looping rather than gathering, and the failure names it.
Without the bound, a plugin bug becomes an endless prompt to a human, which is the one failure mode worse than a hang.

### The load-bearing rule

A surface with nobody attached answers rather than hanging or failing on the plugin's behalf.
It gets a named test: `TestADR0046_AnUnattachedSurfaceAnswersRatherThanHanging`.

## Consequences

### Good

- An action whose input is a judgement can insist a human made it, rather than trusting that one did.
- The same declaration still serves an agent, the UI and a schedule, so [ADR-0041](0041-plugins-reach-agents-as-actions.md) holds.
- `Invoke` stays unary and the change is additive, so existing plugins are untouched.
- A plugin can distinguish a considered `decline` from a `cancel` from `unsupported`, and behave differently for each.

### Bad

- An action can now take several round trips through the plugin, and a plugin author has to make it resumable. The token exists to make that cheap, but "resumable" is a real constraint that unary actions did not carry.
- A chained step cannot ask. The caller is already holding the result of the action that started the chain, so a step that asks is told `unsupported` and decides for itself. Composition and elicitation therefore do not combine, which will surprise somebody eventually.
- Four turns is arbitrary. It is generous enough for any real interview and too small for none, but it is a number in a constant rather than a reasoned limit.
- Dusk now relays a schema it does not validate. A plugin can ask for a shape the client cannot render, and neither side finds out until a human sees a broken form.
- The `preview` path deliberately does not elicit, so a dry run of an asking action shows what would happen without the answers. That is defensible and it is also a second behaviour to explain.

## Amendments

None yet.
