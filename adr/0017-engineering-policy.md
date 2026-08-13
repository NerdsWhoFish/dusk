# 17. Engineering policy for NerdsWhoFish repositories

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk is intended to be adopted and extended by people who did not write it, under a licence that invites them to, per [ADR-0003](0003-license.md).
That makes conventions a public interface rather than a private preference.

It also has unusual stakes.
Dusk is a catalog people are asked to trust, and its failure modes are quiet ones: a stale entity that looks current, a duplicate note nobody prunes, an ingester timeout mistaken for a decommissioned service.
None of those crash. All of them erode the only thing the product sells, which is that the catalog is correct.

Conventions that live only in reviewers' heads get applied inconsistently and argued about repeatedly.
This ADR writes them down once, and prefers mechanisms over good intentions wherever one exists.

## Considered Options

1. **No written policy**, relying on review to catch drift.
2. **A style guide document** with no enforcement.
3. **A policy ADR with CI enforcement** wherever a rule is mechanisable.

## Decision Outcome

Chosen: **option 3**.

### Go is the language, and Go conventions are the conventions

Go, tracking the latest stable release.
Where Go has an established answer, that answer wins over personal preference.
[Effective Go](https://go.dev/doc/effective_go) and [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) are the reference.

In particular:

- Accept interfaces, return concrete types.
- Interfaces are small and defined by the consumer, not exported speculatively by the producer.
- `context.Context` is the first parameter of anything that blocks, and is never stored in a struct.
- Errors are wrapped with `%w` and inspected with `errors.Is` and `errors.As`. Library code does not panic.
- No package-level mutable state.
- Package names are short, lowercase, and meaningful. There is no `util`, `common`, `helpers`, or `misc` package, because those are places code goes to avoid being named.
- `gofmt` is not negotiable, and `golangci-lint` runs in CI.

### No cgo without an ADR

Any dependency that requires cgo needs its own ADR arguing the case.

This is not stylistic.
cgo costs cross-compilation, distroless and Alpine images without a C toolchain, clean `go test -race`, and reproducible builds, and Dusk ships as a multi-architecture binary and container.
[ADR-0008](0008-storage.md) already turns on this exact property, where the default GORM SQLite driver would have quietly reintroduced cgo and taken arm64 cross-compilation with it.

The failure mode is that cgo arrives as a transitive dependency nobody chose.
Requiring an ADR makes it a decision rather than an accident.

### Packages are the unit of reuse, and `internal/` needs a reason

Default to a real package with a real name.
Write it so another caller could use it, and export what that caller would reasonably want, even when no such caller exists yet.

- **`pkg/`** holds anything that could plausibly be used outside the repository. This is a NerdsWhoFish convention rather than a Go standard, adopted because paired against `internal/` it states intent unambiguously: `pkg/` is a promise, `internal/` is a fence.
- **`internal/`** is for code that genuinely should not be imported elsewhere, and choosing it is a decision. "I have not thought about reuse yet" is not a reason to put something there, because the compiler will not let you change your mind cheaply once other things depend on the shape.
- **`cmd/`** holds binaries and stays thin. A command parses flags, wires dependencies, and calls into a package. Logic in `cmd/` is logic nothing else can reach and nothing can test through its real interface.

Package names are short, lowercase, and describe what the package provides.
A package is a coherent thing with a reason to exist, not a folder for loosely related files.

**A second consumer promotes a package to its own module.**
Importing `dusk/pkg/foo` from another repository drags the whole `dusk` module and its entire dependency graph along with it, which is a real cost that arrives quietly.
So `pkg/` is the staging ground: the moment a second repository actually depends on something there, it moves to its own module, as [ADR-0016](0016-plugin-sdk-repo.md) already did for the plugin contract.

Until there is a second consumer, splitting early is premature.

### Reuse before writing, and know where DRY stops

The default assumption is that the thing you need already exists.
Search before writing, extend an existing abstraction before standing up a parallel one, and export what another caller would reasonably want.

A second implementation of something that already exists is the most expensive kind of mistake, because nothing fails.
It quietly rots into two things that drift apart.

**DRY has a limit, and Go is right about where it is.**
A little duplication is cheaper than the wrong abstraction.
Two things that look alike today but answer to different reasons for changing are not duplication, and merging them creates a coupling that has to be torn apart later at greater cost.
De-duplicate when the two copies genuinely change together, not when they merely resemble each other.

When reuse is genuinely not possible, say so explicitly and say what was considered.
That sentence is how a reviewer catches it when the judgement is wrong.

### Low complexity, measured rather than asserted

Functions do one thing and are short enough to read without scrolling.
Nesting is flattened with early returns rather than accumulated in `else` branches.

Complexity is linted rather than left to taste, because "keep it simple" without a number is unenforceable and gets waived under deadline.
A function that exceeds the limit either gets decomposed or gets a comment explaining why it is genuinely irreducible.

### Everything is documented, in `docs/`

Every repository has a `docs/` directory of markdown, and every subsystem, feature, and operational procedure has a document there.

- `README.md` is the entry point: what this is, how to run it, where to go next.
- `docs/` holds the substance, one document per subsystem.
- `adr/` holds decisions and the alternatives that lost.
- Go doc comments cover every exported identifier and every package, in standard godoc form.

**When a document is added, moved, or deleted, the index is updated in the same change.**
An index that lies is worse than no index, because it sends readers to files that do not exist.

Prose is one sentence per line.
Renderers join adjacent lines into a paragraph, so hand-wrapping buys nothing and costs real money in diffs.

#### Documentation is not the same thing as commentary

These two rules look contradictory and are not.

**Document exported API and system behaviour exhaustively.** A doc comment on an exported identifier and a markdown file for a subsystem are documentation, and their absence is a defect.

**Comment implementation with restraint.** An inline comment restating what the code already says is noise. Comment the non-obvious *why*: a workaround, a gotcha, a constraint that is not visible locally. If it needs paragraphs, it belongs in `docs/` with a link.

A wrong comment is worse than no comment, so a comment that no longer matches its code gets fixed or deleted in the same change.

### Testing

#### Test the observable result, not the implementation

**The default is to assert what a consumer actually gets, not how the code arrived at it.**

For Dusk that means the response an agent receives from an MCP tool, the page a human sees, the commit that lands in the repository, the error message returned.

Two consequences follow, and they are the point of the rule.
A refactor that preserves behaviour should not break tests, which is what makes the suite an asset rather than a tax on changing anything.
And a passing test should mean the user-visible thing works, rather than meaning the internals were arranged as expected on the day the test was written.

Testing internals is a **deliberate exception, not a prohibition.** Reach for it when the behaviour is genuinely hard to reach from outside and the cost of being wrong is real: a parser, a canonicaliser, a hash, a retry or backoff path, a concurrency invariant that cannot be provoked reliably through the public surface.
The bar is a reason, stated in the test, not permission.

Mocks follow the same shape.
Prefer them at genuine external boundaries such as the GitHub API or a plugin process, and be wary of a test assembled from mocks of your own components, because that asserts the design rather than the behaviour and will keep passing after the behaviour breaks.
Sometimes mocking an internal boundary is the only sane way to force an error path, and that is fine when it is chosen rather than defaulted to.

#### Table-driven by default

Tests are table-driven with `t.Run` subtests unless there is a specific reason not to be.
A single-case test that is likely to grow a second case starts as a table.

This is a readability rule before it is a coverage one.
A table states the cases as data, so what is and is not covered is visible by reading the table rather than by reading the assertions, and adding a case is a line rather than a copied block.
Subtests also mean a failure names the case, so `attributes/nested_struct` locates the problem without opening the file.

Each case carries a `name` that describes the behaviour, not the input.

#### Every ADR invariant has a named test

Any rule an ADR states as load-bearing gets a test named after it.

```text
TestADR0011_FailedIngestDoesNotDelete
TestADR0009_WriteWithoutProofTokenIsRejected
TestADR0006_PollFloorRunsWithWebhooksConfigured
```

The rules that matter here are not "this function returns the right value", they are "a failing ingester must never look like a deletion".
Naming the test after the decision makes the invariant executable and traceable, and makes deleting it an obviously deliberate act rather than a cleanup.

#### Conventions

- **Standard library `testing` only.** No testify, no go-cmp, no assertion libraries. Assertion libraries obscure failure output and add a dependency to every package.
- **`t.Helper()` on every helper**, so failures point at the call site rather than the helper.
- **`t.TempDir()` for filesystem work.** Never a shared or hard-coded temp path.
- **`-race` always**, in CI and locally. Reconcile, ingesters, and the commit queue all run concurrently.
- **No network in unit tests.** Anything requiring a live service goes behind a build tag.

#### A skipped test is a failed test in CI

Tests that silently skip when a dependency is missing report success while testing nothing.
CI sets an environment variable that converts those skips into failures, so a misconfigured runner is loud rather than green.

#### The plugin contract ships a conformance suite

Per [ADR-0016](0016-plugin-sdk-repo.md), plugin authors get an importable conformance package so "is my plugin correct" has a definitive answer that does not involve reading the server.

The documented example in the SDK README is itself a test.
If the example a plugin author copies does not produce a valid batch, the language-agnostic claim in [ADR-0002](0002-plugin-protocol.md) is false.

#### No coverage threshold

Coverage is reported, not gated.

### The GitHub API budget is a design constraint, not an implementation detail

Every design that touches GitHub states what it costs per repository per sweep, and the cheap case is the one that has to be cheap.

A GitHub App installation gets on the order of 5,000 requests an hour, shared by every repository it can see and by webhooks, reconciles and writes alike.
That number does not grow with how interesting the installation is.
An operator with ninety repositories and two participating ones is the normal case ([ADR-0027](0027-design-target.md)), so the cost of the eighty-eight that declare nothing is the cost that matters, and it should be as close to nothing as the API allows.

Three rules follow, and they are why [ADR-0032](0032-tarball-reads.md) reads a tarball and [ADR-0006](0006-reconcile-triggering.md) can afford a slow floor.

- **Per-file is a smell.** A cost that scales with how much a repository declares punishes exactly the users who adopted the thing hardest. Fetch the tree.
- **Gate before you spend.** Confirm a repository participates, and that its commit moved, before reading anything from it. An unchanged repository costs one request; a non-participating one costs one and downloads nothing.
- **Exhausting the budget is a correctness bug, not a slowdown.** Every request past the limit fails, so a wasteful sweep does not run late, it makes the catalog wrong until the hour rolls over. Treat a rate-limit response as an incident in the logs, not a retry statistic.

The rate-limit headers are read and logged rather than ignored, because an install approaching its ceiling has to be visible before it hits it.

## Consequences

### Good

- Conventions written once stop being re-litigated per pull request, and a contributor can read the rules instead of inferring them from review comments.
- Requiring an ADR for cgo turns a transitive dependency into a visible decision, protecting cross-compilation and the multi-architecture build that the distribution story depends on.
- Making `internal/` a decision rather than a default preserves reuse that would otherwise be lost by reflex, and the promotion rule stops `pkg/` from turning one repository into an accidental library everyone else depends on.
- Naming the limit of DRY prevents the more common failure, which is not duplication but a premature abstraction that couples two things that should have stayed apart.
- Linted complexity is enforceable in a way that "keep it simple" is not, and it survives deadline pressure.
- Separating documentation from commentary resolves a contradiction that would otherwise produce either undocumented exported API or narrated implementation.
- Testing observable results means the suite survives refactoring, so it stops being a reason to avoid changing the design.
- Invariant tests protect the specific failures that would destroy trust, which a coverage percentage does not.
- Table-driven by default means the covered cases are readable as a list, which makes a gap visible during review rather than discoverable only in production.
- Failing on skips removes an entire category of false green.

### Bad

- A long policy is a long policy. Some of it will not be read, and the parts CI does not enforce will drift.
- Requiring an ADR for cgo will occasionally block something reasonable behind paperwork, and someone will be annoyed by that at exactly the wrong moment.
- `pkg/` is a convention borrowed from a layout the Go team does not endorse, and it adds a directory level carrying no semantic meaning of its own. It is adopted for the signal it sends next to `internal/`, and that is a trade rather than a free win.
- Defaulting to exported means designing interfaces for callers who may never exist, which is work that is sometimes wasted.
- "A little duplication is better than the wrong abstraction" is a judgement call, and judgement calls get argued over in review.
- A complexity limit produces false positives. Some genuinely irreducible functions will need a waiver, and waivers erode if granted freely.
- Documenting every subsystem in `docs/` is real ongoing work, and stale documentation is worse than absent documentation.
- Standard library assertions are more verbose than testify, and that verbosity is a real ongoing cost.
- Table-driven by default is wrong for some tests. A case needing distinct setup gets forced into a struct field that most rows leave empty, and the table becomes harder to read than the thing it replaced. "Unless there is a specific reason not to" is doing real work in that rule.
- Testing through the boundary makes failures harder to localise. A broken assertion says the output is wrong without saying which layer broke it, and that debugging cost is paid every time.
- Named invariant tests require discipline that nothing enforces automatically. A new ADR with no test will not fail CI.
- Declining a coverage gate means coverage can quietly decay, with only review to catch it.

### Rejected because

- No written policy was rejected because it makes conventions depend on who reviews, which is neither fair to contributors nor stable over time.
- A style guide with no enforcement was rejected because unenforced rules are aspirations. Where a rule can be mechanised it is, and where it cannot be, the ADR says so plainly rather than pretending.
- A coverage threshold was rejected because it measures lines executed rather than behaviour verified. It is satisfiable by tests that assert nothing, and it applies pressure toward testing easy code rather than dangerous code.

## Amendments

### 2026-08-11: the API budget is a policy concern

Added "The GitHub API budget is a design constraint, not an implementation detail".

The policy covered how code is written and tested but said nothing about what it costs to run, and the gap showed: the reconcile path shipped reading one file per request, which was fine for one repository and untenable across ninety.
[ADR-0032](0032-tarball-reads.md) fixed that instance. This records the general rule so the next design does not have to rediscover it.
