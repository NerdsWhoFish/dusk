# 54. A plugin whose process dies is restarted, and one that will not stay up is left failed

Date: 2026-08-15

## Status

Accepted

## Context and Problem Statement

[ADR-0039](0039-one-plugin-transport.md) made every plugin a subprocess speaking gRPC over a socket the host provides, and named the price in its own Bad consequences: "every plugin now costs the host process lifecycle, health and cleanup".
None of that was built.

So a plugin whose process dies stays dead.
Dusk keeps a client connected to a socket nothing is listening on, every call fails with a transport error naming a path and a syscall, and the only way back is restarting Dusk.
[ADR-0015](0015-plugin-actions-and-events.md) made that worse than it sounds: a plugin is no longer only a source of observations, it is also how an action reaches a system, so a crash takes capability down alongside freshness.

Three questions have to be answered together, because answering them apart produces something that either loops or gives up:

- Whether Dusk restarts a plugin at all, or fails loudly and waits for a human.
- How hard it tries, and what stops a crash loop becoming a hot loop.
- What a restart must never do to the catalog on its way past.

The third is the constraint the other two fit around.
[ADR-0011](0011-ingester-scheduling.md) forbids conflating "I could not look" with "it is not there", and `ingest.Run` treats an `Observation` as a **complete** view of its source: anything absent from it is deleted from that scope.
An observation returned during a restart would therefore not be a gap in the catalog.
It would be the deletion of everything that plugin had ever observed, by the machinery built to keep it alive.

## Considered Options

1. **Fail loudly.** Report the dead process clearly and leave restarting to a human.
2. **Restart on demand.** Notice the process is gone the next time something calls it, and start it then.
3. **Supervise the process.** Watch it exit, restart with capped exponential backoff, and stop after a limit.
4. **Delegate to a process supervisor outside Dusk**, such as systemd or a container per plugin.

## Decision Outcome

Chosen: **option 3**.

### The exit is the trigger

One goroutine per plugin waits on `cmd.Wait` and decides what the exit meant.
Nothing polls, because a process ending is something the operating system already tells us, and a poll would add a window in which the plugin is dead and Dusk does not know it.

That wait had to become the only one.
`Running.stop` used to call `Wait` itself, and a second `Wait` on one `exec.Cmd` loses the race and gets "Wait was already called" instead of the exit status.
The single waiter closes a channel, and everything that needs to know whether the process is there reads that.

A stop Dusk asked for is not a crash.
Uninstalling, reconfiguring, updating and shutting down all close the plugin's supervision before the process is signalled, so nothing restarts what somebody just removed.

### Backoff, a cap, and a limit

Doubling from a second, capped at a minute, eight attempts: a little under four minutes of trying.

The cap is what stops a crash loop being a hot loop.
The limit is what stops it being an infinite one.
A plugin that exits immediately on start would otherwise be re-executed for ever at whatever rate the cap allowed, which is a machine spending itself on a decision nobody made.

A process that stayed up for longer than a minute resets the count.
Without that, a plugin that has served for a week is given up on the second time it ever crashes, because the two failures are adjacent in the counter and a year apart in fact.

### Failed is a state, and there is a way out of it

A plugin that exhausted its attempts is `failed`, which the plugin's page shows and `get plugin:name` says.
"Crashed once and came back" is a running plugin with a restart count and a last exit; "has never stayed up" is a failed one with its attempts spent.
The difference is the question somebody actually has, and neither is answerable from "not running".

`Restart` is the way back, and it is not a convenience.
`Configure` refuses when a plugin is not running, because only a running plugin can say which of its fields are sensitive ([ADR-0023](0023-plugin-configuration.md)), so without a restart control the only remedy for a failed plugin is uninstalling it and losing its configuration.

### A dead process answers with an error, never with an emptiness

This is the load-bearing rule, and it is tested as `TestADR0054_ARestartIsNeverAnEmptyObservation`.

Every call that crosses the socket asks whether the process is there first, and the ingest path asks **twice**: once before the stream is opened, and once when it ends.
In principle the second check is redundant, because gRPC reports a broken transport as `Unavailable` rather than as a clean `io.EOF`.
It is there because the two mistakes are not the same size.
Reporting a failure for a complete observation costs one interval of staleness; reporting a complete observation for a failure deletes a source.

The consequence is deliberate and points the safe way: a process that dies immediately after its last batch is reported as a failed run even though nothing was lost.

### An interrupted action says what is not known

A plugin dying while an action runs is different from one that was already dead.
The caller gets an error naming the exit rather than a transport failure, and for a mutating action it says the outcome is **not known**, because the process that could have said whether the change landed is the one that went away.

Dusk records the event as failed, which is not quite true.
`EventStatus` has no word for unknown, and adding one is a change to a published contract in another repository for a case this ADR can carry in the message instead.

### One socket directory per Dusk

Sockets are named `<SocketDir>/<plugin-id>.sock`, so two Dusks on one machine bind the same paths and each removes the other's on start and on stop.
That was already recorded as a gap, and a supervisor makes it materially worse: it was a collision at install and at boot, and it becomes a collision at any moment, because restarting is exactly the act of unlinking and rebinding.

Each Dusk now mints a directory under `SocketDir` at its first start and removes it on shutdown.
Sockets are per plugin within it, which keeps [ADR-0044](0044-plugins-keep-the-socket-directory.md) intact: that decision rejected a directory per *plugin* as isolation theatre under one user, and said nothing about two hosts sharing a namespace, which is what this is.

## Consequences

### Good

- A crash is an interruption rather than an outage, which is what a catalog whose claim is that it maintains itself has to mean.
- Nothing polls. The restart begins when the process ends, not when something next asks.
- Observations survive a restart untouched, because the scheduler's failed runs store nothing and remove nothing. The catalog goes stale and says so, which is exactly [ADR-0011](0011-ingester-scheduling.md)'s designed behaviour.
- A plugin that starts again is due immediately, so the view it serves is refreshed rather than waiting out its interval.
- What a plugin printed as it died now survives into the process that replaces it, which is where the reason for the crash almost always is.
- A start that fails is supervised the same as a process that died, so a plugin that never came up at boot is retried and then reported rather than logged once and forgotten.

### Bad

- **Restarting hides a bug.** A plugin that leaks and dies every hour reads as running with a number beside it, and a number nobody reads is how a fault becomes permanent. Making the count visible is the whole mitigation, and it is a weak one.
- **Four minutes is a guess.** A plugin whose upstream is unreachable for five minutes at boot ends up failed and needs a human, even though waiting would have fixed it. Any number here is wrong for somebody.
- **A mutating action interrupted mid-flight has an unknowable outcome**, and Dusk records it as failed. Anything that retries on the strength of that record can apply an effect twice.
- **Two backoff curves now govern one plugin.** The ingest breaker paces a plugin that is up and failing every observation; this one paces a plugin that is not up. They are deliberately separate, because one is about an upstream system's budget and the other about exec, and a reader will reasonably expect them to be the same thing.
- **A directory per Dusk leaks on an unclean exit.** A killed Dusk leaves its socket directory behind, where the shared name was at least reused. Nothing sweeps them, because sweeping is indistinguishable from removing a live Dusk's sockets, which is the bug being fixed.
- **A clean shutdown part way through an observation reads as a failed run.** The liveness check at the end of the stream cannot tell that case from a crash, and it is not trying to.
- Dusk now has a supervisor, which is a category of software whose failure mode is starting things nobody wanted.

### Rejected because

- **Option 1** was rejected because it is the current behaviour and the reason this ADR exists. An unattended catalog stays broken until somebody notices, and what a broken plugin looks like from outside is a catalog that is quietly out of date, which is the failure mode [`docs/philosophy.md`](../docs/philosophy.md) calls the strongest rule in the document.
- **Option 2** was rejected because the caller that would trigger a restart is usually the scheduler, and the scheduler's call is precisely the one that must not come back with an empty view. Restarting inside it means either blocking a run on a process start or returning something while the plugin is mid-start, and the second is the deletion this decision exists to prevent. It also makes the first action after a crash the thing that discovers the crash, and pay for it.
- **Option 4** was rejected because a supervisor outside Dusk cannot see a plugin at all. A plugin is a subprocess of one Dusk process ([ADR-0039](0039-one-plugin-transport.md)) run from a binary Dusk installed at runtime ([ADR-0042](0042-installing-plugins.md)), so making it a supervised unit means it is no longer a subprocess. In Kubernetes that is a container per plugin, which is a network transport, which is [ADR-0039](0039-one-plugin-transport.md)'s option 5 arriving by the back door with its authentication and registration ceremony intact.
