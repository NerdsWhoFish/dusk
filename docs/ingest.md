# Ingestion

A declaration is what somebody wrote in a `dusk.md`.
An observation is what Dusk found by looking.

Ingestion is the half of the catalog nobody types, and it is what makes "a service catalog that maintains itself" true rather than aspirational.

## What an ingester is

One source system, and a promise about failure.

```go
type Ingester interface {
    Name() string
    Interval() time.Duration
    Observe(ctx context.Context) (*Observation, error)
}
```

An `Observation` is **complete by contract**: anything previously observed and absent from it is treated as gone.
A partial view has to be an error instead, because the alternative is deleting a catalog because one API call was slow.

**Every ingester is now a plugin.**
Kubernetes was built in tree first, deliberately, so the protocol was designed against something that already worked rather than against a guess ([ADR-0034](../adr/0034-ingesters-in-tree-first.md)).
It moved out to `dusk-plugin-kubernetes` once that was true, and core carries no ingester at all ([ADR-0040](../adr/0040-core-and-plugins.md)).

What stayed is this package: the interface, the completeness contract, the scheduler, and the never-delete rule.
A plugin joins the same rotation as an ordinary `Ingester`, so none of that is reimplemented per plugin, and a plugin cannot opt out of it.

## Failure never deletes

An error means "I could not look", never "it is not there".
A failed run stores nothing and removes nothing.
The previous observation stays, with the `observed_at` it was last seen with, so staleness is derived rather than tracked separately ([ADR-0011](../adr/0011-ingester-scheduling.md)).

This is the rule most easily broken by an implementation that clears a scope before it knows the run succeeded, and it has a named test: `TestADR0011_FailedIngestDoesNotDelete`.

A source that cannot be reached at all still runs, and reports the failure every pass.
Refusing to start would take the whole catalog down over one bad credential, and the catalog is still correct about everything else.

That failure is visible rather than only logged: each instance carries its last error and how many runs have failed in a row, which the plugins page shows on the row.

## Where observations live

Under a reserved scope: `ingester:<name>` in the repository slot, at the ref `refs/dusk/observed`.

That falls out of the index already being partitioned by `(repository, git ref)`.
It also means declared and observed are directly comparable, which is what drift needs.

**Anything treating a repository as clonable, or as something a sweep may prune, has to check `index.IsObserved` first.**
The sweep did not, once, and deleted every observation a minute after the first one arrived.

Where a ref exists in both halves, **the declared one wins a read**: a person wrote it deliberately, an ingester inferred it.

## Scheduling

Each ingester declares its own interval.
A central scheduler runs what is due, at most `Concurrency` at a time so a restart does not stampede every source at once.

Failure backs off exponentially from the ingester's own interval, capped at 30 minutes, and after 8 consecutive failures it stays there.
One broken source cannot spend the budget the others need.

Not built: the shared per-source API budget from [ADR-0011](../adr/0011-ingester-scheduling.md).
Each ingester is bounded only by its own interval, so two GitHub ingesters would each assume they had the whole quota.

## Where the ingesters went

Install one from the plugins page.
A plugin is configured there too, in a form rendered from the fields it declares, so Dusk holds no per-plugin configuration and there is no environment variable to set ([ADR-0023](../adr/0023-plugin-configuration.md), [ADR-0042](../adr/0042-installing-plugins.md)).

One plugin may be configured several times, each instance with its own scope and its own place in the rotation.
Two clusters are two instances of one plugin rather than two installs, and they fail apart.

Normalization is the plugin's job, not Dusk's ([ADR-0018](../adr/0018-normalization-at-the-edge.md)).
A plugin decides what a ref looks like, what is worth cataloguing at all, and what is machinery a reader would never describe.
Dusk never re-derives any of it, which is why adding a plugin cannot change how an existing one is read.

## Saying which observed thing is yours

A human and an ingester never independently pick the same name.

```yaml
observed_as:
  - service:prod/media-jellyfin
```

Without the mapping, drift reports your declaration as missing and the observed service as undeclared, forever.
See [dusk-md.md](dusk-md.md).

The mapping is unnecessary when you declare an entity at the ref the ingester already reports, which is the shortest path from the undeclared list to a documented estate.
Declaring `host:prod/node-1` when that is what the ingester calls it makes them the same entity, the declaration wins the read, and no alias is involved.

## What drift will not tell you

Drift only judges kinds something observes ([ADR-0038](../adr/0038-what-drift-may-say.md)).

A Kubernetes plugin reports services, hosts and clusters, so with only that installed a declared `repository:` or `team:` is never reported as missing however long it has been gone.
Nothing is watching for it, and a report that named every unwatched kind would be noise on the first page.

The cost is the reverse case: uninstalling the only plugin that observes a kind makes drift go quiet about that kind rather than raising an alarm.
Watch the plugin's own health for that, not drift.
