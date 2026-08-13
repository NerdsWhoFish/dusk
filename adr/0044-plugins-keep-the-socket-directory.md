# 44. Plugins keep the shared socket directory, and the token stays the boundary

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

Every plugin serves on a unix socket in one directory, owned by one user.
Any plugin can therefore dial any other plugin's socket.
[ADR-0042](0042-installing-plugins.md) closed the practical hole by minting a token per start and having each plugin refuse a call that does not present it, which makes dialing a neighbour useless.

The token is a lock on a door that still exists.
The obvious way to remove the door is to pass each plugin a connected socket as an inherited file descriptor, so there is no path in the filesystem at all, and this was written down as the thing to do next.

The cost was recorded as "changing how a plugin is started".
On inspection that cost is larger than it looked, and lands on the promise this protocol exists to keep.

## Considered Options

1. **A socketpair passed as an inherited descriptor.** No path, no directory, no token needed.
2. **A directory per plugin**, mode 0700.
3. **Unlink the socket once Dusk has connected**, so the path exists for one dial and then does not.
4. **Keep the shared directory and the token.**

## Decision Outcome

Chosen: **option 4**. The directory and the token stay.

### The socketpair costs the language-agnostic promise

[ADR-0039](0039-one-plugin-transport.md) chose a unix socket over stdio for one stated reason, and it is exactly this one: gRPC over an inherited descriptor "is awkward in Python and Node, whose gRPC libraries expect a target address, and a transport that only Go implements comfortably would quietly undo the language-agnostic promise this protocol exists to keep."

Serving gRPC on a pre-connected descriptor is the same shape as serving it on a pipe pair.
It is a few lines in Go and an argument with the runtime in most other languages.
Choosing it would re-decide ADR-0039 by the back door, for a defence in depth behind a control that already works.

### The cheaper options are theatre

**A directory per plugin does nothing.** Every plugin runs as the same user, so filesystem permissions cannot separate them. Mode 0700 keeps out other users, who are not the threat being discussed.

**Unlinking after connect is worse than it looks.** gRPC's channel goes idle and re-dials, so removing the path converts an idle period into a permanent failure, and any transport blip into an unrecoverable one. Making it safe means disabling idle timeouts and adding process supervision to restart a plugin whose connection is gone: real machinery, bought to close a hole the token already closes.

### What the token is and is not

It is not a defence against a hostile plugin.
[ADR-0042](0042-installing-plugins.md) says so plainly, and it remains true: a plugin runs as a subprocess of Dusk, with Dusk's permissions, and can read anything Dusk can read.
The token stops plugins coupling to each other **by accident**, which is the failure this is actually about: an undeclared dependency graph, unversioned, silently skipping when a neighbour is absent, invisible to the catalog.

Composition goes through Dusk, and that is enforced by a plugin refusing to answer anybody else.

## Consequences

### Good

- ADR-0039's transport decision is not quietly reversed for a marginal gain, so a plugin in Python or Node is started the same way as one in Go.
- Nothing changes for existing plugins, and the SDK's `Run` keeps one shape.
- The security property that matters, plugins not composing behind Dusk's back, is unaffected: it was the token doing that work, not the path.

### Bad

- **The path is still there.** A plugin that guesses another's socket name can connect to it, and only the token refuses. That is one control rather than two, and it is a control every plugin implements for itself.
- A plugin that omits the token check is unprotected and nothing detects that. The SDK's `Run` installs it, so the failure needs deliberate effort, but a plugin written against the raw proto could skip it.
- Somebody will read the status item this settles, reach for the socketpair, and have to find this file to learn why not.

### Rejected because

- **The socketpair** was rejected because it makes the transport comfortable only in Go, which is the reason ADR-0039 chose a socket address in the first place. Paying that for defence in depth behind a working control is the wrong trade.
- **A directory per plugin** was rejected because same-user processes cannot be separated by file permissions, so it changes nothing while looking like it does.
- **Unlinking after connect** was rejected because it turns a reconnect into a permanent failure, and making it safe costs process supervision that nothing else needs.
