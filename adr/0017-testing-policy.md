# 17. Testing policy for FetchHQ repositories

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk is a catalog people are asked to trust.
Its failure modes are quiet ones: a stale entity that looks current, a duplicate note nobody prunes, an ingester timeout mistaken for a decommissioned service.
None of those crash. All of them erode the only thing the product sells, which is that the catalog is correct.

That makes testing a higher priority here than the usual argument for it.

A general "write tests" rule is not enforceable and degrades into a coverage number that gets gamed.
Something specific is needed, and it should match conventions already established across the existing Go repositories rather than inventing new ones.

## Considered Options

1. **A coverage threshold** enforced in CI.
2. **A general convention** with no mechanism.
3. **Named invariant tests plus established conventions**, enforced in CI.

## Decision Outcome

### Test the observable result, not the implementation

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

### Every ADR invariant has a named test

Any rule an ADR states as load-bearing gets a test named after it.

```text
TestADR0011_FailedIngestDoesNotDelete
TestADR0009_WriteWithoutProofTokenIsRejected
TestADR0006_PollFloorRunsWithWebhooksConfigured
```

This is the core of the policy.
The rules that matter here are not "this function returns the right value", they are "a failing ingester must never look like a deletion".
Naming the test after the decision makes the invariant executable and traceable, and makes deleting it an obviously deliberate act rather than a cleanup.

### Table-driven by default

Tests are table-driven with `t.Run` subtests unless there is a specific reason not to be.
A single-case test that is likely to grow a second case starts as a table.

This is a readability rule before it is a coverage one.
A table states the cases as data, so what is and is not covered is visible by reading the table rather than by reading the assertions, and adding a case is a line rather than a copied block.
Subtests also mean a failure names the case, so `attributes/nested_struct` locates the problem without opening the file.

Each case carries a `name` that describes the behaviour, not the input.

### Conventions, matching existing repositories

- **Standard library `testing` only.** No testify, no go-cmp, no assertion libraries. Assertion libraries obscure failure output and add a dependency to every package.
- **`t.Helper()` on every helper**, so failures point at the call site rather than the helper.
- **`t.TempDir()` for filesystem work.** Never a shared or hard-coded temp path.
- **`-race` always**, in CI and locally. Reconcile, ingesters, and the commit queue all run concurrently.
- **No network in unit tests.** Anything requiring a live service goes behind a build tag.

### A skipped test is a failed test in CI

Tests that silently skip when a dependency is missing report success while testing nothing.
CI sets an environment variable that converts those skips into failures, so a misconfigured runner is loud rather than green.

### The plugin contract ships a conformance suite

Per [ADR-0016](0016-plugin-sdk-repo.md), plugin authors get an importable conformance package so "is my plugin correct" has a definitive answer that does not involve reading the server.

The documented example in the SDK README is itself a test.
If the example a plugin author copies does not produce a valid batch, the language-agnostic claim in [ADR-0002](0002-plugin-protocol.md) is false.

### No coverage threshold

Coverage is reported, not gated.

## Consequences

### Good

- Testing observable results means the suite survives refactoring, so it stops being a reason to avoid changing the design. It also means a green suite is evidence the product works rather than evidence the internals match a snapshot.
- Invariant tests protect the specific failures that would destroy trust, which a coverage percentage does not.
- Naming tests after ADRs makes the link between decision and enforcement legible in both directions, and surfaces when a decision is reversed without its test being reconsidered.
- Standard library only means no dependency churn in test code and failure output that reads the same in every package.
- Table-driven by default means the covered cases are readable as a list, which makes a gap visible during review rather than discoverable only in production.
- Failing on skips removes an entire category of false green.
- Testing the documented example keeps the README honest as the contract evolves.

### Bad

- Testing through the boundary makes failures harder to localise. A broken assertion says the output is wrong without saying which layer broke it, and that debugging cost is paid every time.
- "Deliberate exception, not prohibition" is a judgement call, and judgement calls get argued over in review. That is preferable to either a suite coupled to the implementation or a rule so absolute that people quietly break it.
- Named invariant tests require discipline that nothing enforces automatically. A new ADR with no test will not fail CI.
- Standard library assertions are more verbose than testify, and that verbosity is a real ongoing cost.
- Table-driven by default is wrong for some tests. A case needing distinct setup gets forced into a struct field that most rows leave empty, and the table becomes harder to read than the thing it replaced. "Unless there is a specific reason not to" is doing real work in that rule.
- No `t.Parallel()` in the existing convention means slower suites as they grow.
- Declining a coverage gate means coverage can quietly decay, with only review to catch it.
- Build-tagged integration tests do not run by default, so they rot unless CI runs them somewhere.

### Rejected because

- A coverage threshold was rejected because it measures lines executed rather than behaviour verified. It is satisfiable by tests that assert nothing, and it applies pressure toward testing easy code rather than dangerous code.
- A general convention with no mechanism was rejected because it is what every project believes it has, and it is why this ADR is necessary.
