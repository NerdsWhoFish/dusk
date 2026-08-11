# 3. Apache 2.0, with a DCO rather than a CLA

Date: 2026-08-11

## Status

Accepted. Amended, see [Amendments](#amendments).

## Context and Problem Statement

Dusk is intended to be adopted by people running their own infrastructure ([ADR-0027](0027-design-target.md)), and its plugin ecosystem depends on third parties writing integrations without legal friction.
A license also has to stay unobjectionable to a contributor's employer, which is frequently the party that actually reads it.
All of those are shaped by the license before a single line of code is written.

There is also relevant context in this ecosystem.
The IaC and platform tooling community watched HashiCorp relicense under the BSL, and watched the OpenTofu fork that followed.
That history means license choices in this space are read as signals, not just legal terms.

## Considered Options

1. **MIT**
2. **Apache 2.0**
3. **AGPL 3.0**
4. **BSL, SSPL, or Elastic License** (source-available)
5. **Open core**: permissive core with proprietary paid components

## Decision Outcome

Chosen: **Apache 2.0**, with a **DCO** for contributions.

The entity JSON schema and the plugin `.proto` are explicitly covered and called out in the plugin documentation, so plugin authors have no ambiguity about what they are compiling against.

If commercial work happens later, the route is a hosted or managed offering, not a license change.

### Consequences

#### Good

- The explicit patent grant matters for software that companies will run internally, and it is the reason for Apache 2.0 rather than MIT.
- It is the default in this space, which removes license as a factor in adoption decisions. Backstage and the CNCF projects Dusk will be compared against are all Apache 2.0.
- A DCO is a sign-off, not an assignment, so individual contributors are not asked to hand over rights before their first patch.
- Publishing the schema and `.proto` under the same license removes the most common source of plugin-author hesitation.

#### Bad

- Apache 2.0 permits a cloud provider to host Dusk commercially without contributing back, and that option is given up deliberately.
- A DCO makes relicensing effectively impossible without contacting every contributor. That is a real loss of optionality, and it is accepted as the price of trust.
- Apache 2.0 carries more ceremony than MIT: a NOTICE file, header conventions, and slightly more contributor friction.

#### Rejected because

- MIT saves nothing here. It is simpler, but it lacks the patent grant, which is the specific protection corporate adopters care about.
- AGPL would kill the target audience. Many companies ban AGPL outright, and Dusk is precisely the kind of tool they would run internally, so the copyleft would deter the adopters it needs in exchange for protection against a hosting competitor that does not yet exist.
- BSL, SSPL, and the Elastic License would land badly with the exact community being recruited from, given the OpenTofu history, and the reputational cost would arrive immediately while the commercial benefit would remain hypothetical.
- Open core is not rejected permanently, only deferred. It is a decision that requires a product with users, and revisiting it later costs nothing that deciding it now would save.

## Amendments

Amendment policy: [ADR-0028](0028-amending-adrs.md).

### 2026-08-11: the audience in the context statement

The opening sentence named "platform teams inside companies" as the intended adopters.
[ADR-0027](0027-design-target.md) settled the design target as a single operator and their agents, so that sentence now names people running their own infrastructure, and states separately the argument it had been carrying implicitly: a license has to stay unobjectionable to a contributor's employer.

The decision outcome is untouched, and so is the rejected-options reasoning.
"AGPL would kill the target audience" still argues from corporate adoption, and that is left exactly as accepted.
It remains a true reason to avoid the AGPL, and rewriting an argument to match a later decision is precisely what ADR-0028 forbids.
