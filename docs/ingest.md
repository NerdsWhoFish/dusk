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

Ingesters live in tree for now, ahead of the plugin protocol ([ADR-0034](../adr/0034-ingesters-in-tree-first.md)).
The machinery is the hard part and the protocol is a second implementation of it.

## Failure never deletes

An error means "I could not look", never "it is not there".
A failed run stores nothing and removes nothing.
The previous observation stays, with the `observed_at` it was last seen with, so staleness is derived rather than tracked separately ([ADR-0011](../adr/0011-ingester-scheduling.md)).

This is the rule most easily broken by an implementation that clears a scope before it knows the run succeeded, and it has a named test: `TestADR0011_FailedIngestDoesNotDelete`.

A source that cannot even be reached at boot becomes an ingester that reports that failure.
Refusing to start would take the whole catalog down over one bad credential, and the catalog is still correct about everything else.

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

## The Kubernetes ingester

Point it at clusters with `DUSK_KUBERNETES`:

```console
DUSK_KUBERNETES=mini-2                          # in-cluster credentials
DUSK_KUBERNETES=mini-2,mini-1=/etc/dusk/mini-1  # and one over a kubeconfig
```

**The context is the cluster's own name**, not the kubeconfig's `current-context`, and a missing one is an error listing what is available.
Falling back would observe one cluster and label everything with the name of another, which is the quiet kind of wrong this project exists to avoid.

In-cluster credentials need the ServiceAccount token mounted and read access to nodes and services.
Nothing else: a catalog has no business reading secrets or pod specs.

### What it leaves out

A catalog is for things somebody would describe, and every row of machinery stands between the reader and something real.

Skipped namespaces: `kube-system`, `kube-public`, `kube-node-lease`, `flux-system`, `cnpg-system`, `cert-manager`, `local-path-storage`.

Skipped services:

- **Headless**, which back a StatefulSet rather than being a thing themselves.
- **ExternalName**, which alias something already catalogued.
- **`default/kubernetes`**, which is the API server.

On one real cluster this is the difference between 33 entities and 18, and the 18 are the ones worth declaring.

### Naming

Refs are `service:<cluster>/<namespace>-<name>`, `host:<cluster>/<node>`, `cluster:<cluster>/<cluster>`.

Normalization happens here rather than downstream ([ADR-0018](../adr/0018-normalization-at-the-edge.md)).
It is ugly where a namespace and service share a name, giving `karakeep-karakeep`, which is the cost of a scheme that cannot collide.

## Saying which observed thing is yours

A human and an ingester never independently pick the same name.

```yaml
observed_as:
  - service:mini-2/media-jellyfin
```

Without the mapping, drift reports your declaration as missing and the observed service as undeclared, forever.
See [dusk-md.md](dusk-md.md).
