# 23. Plugins declare typed configuration, and sensitive values never enter git

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

A plugin needs configuration: which cluster, which token, which namespace to watch.
Only the plugin knows what it needs, so Dusk cannot hard-code a form per plugin.

Two constraints collide.

[ADR-0013](0013-layout-and-pages.md) says configuration lives as markdown in the config repository, which makes it declarative, reviewable, and GitOps-managed.
A plugin's API key obviously cannot live in git.

And accepting sensitive configuration changes what Dusk is.
Once a plugin declares a credential field, **Dusk becomes a secret broker handing third-party code the operator's credentials.** A malicious or careless plugin exfiltrates that trivially, and no amount of storage encryption prevents it, because the plugin must receive the value to use it.

## Considered Options

For the schema:

1. **JSON Schema**, consistent with `ActionDescriptor.params_schema`.
2. **A typed list of field descriptors.**

For storage:

1. Everything in the encrypted store.
2. Everything in markdown.
3. **Split by sensitivity.**

## Decision Outcome

### Configuration is a typed field list, not JSON Schema

`DescribeResponse` gains `config_fields`: name, label, help text, type, required, default, validation, and **`sensitive`**.

A typed list is chosen over JSON Schema specifically because of `sensitive`.
JSON Schema can express it through `writeOnly` or `format: password`, but a security-critical property should not be a nested annotation an author can forget and a renderer can overlook.
As a first-class field it can be enforced by the conformance suite.

It also means the admin UI renders from a typed description rather than needing a JSON Schema form library, which is the same approach [ADR-0020](0020-plugin-ui.md) takes to plugin views.

Action parameters keep JSON Schema. Those are agent-facing call arguments; these are human-facing form fields. The inconsistency is accepted, not overlooked.

### Storage splits on sensitivity

- **Non-sensitive configuration is markdown frontmatter** in the config repository, exactly as [ADR-0013](0013-layout-and-pages.md) requires. Declarative, diffable, reviewable.
- **Sensitive values live in the encrypted store** from [ADR-0022](0022-credential-encryption.md), and the markdown references them by name.

This is the shape of a Kubernetes Secret reference, and it keeps everything reviewable in git while keeping everything secret out of it.

### Sensitive values are write-only

A sensitive value can be set and replaced, never read back.
The API returns a masked sentinel, and it does so for administrators and for the UI, not only for ordinary callers.

They are never logged, never in events, never in the entity graph, and carry the redacting type from ADR-0022.
They reach the plugin only over the unix socket at call time.

### `ValidateConfig` before saving

Plugins implement a `ValidateConfig` RPC that exercises the configuration and reports a specific result: reached the cluster, or a named failure.

Configuration that is wrong should say so when it is entered rather than as a mysterious ingest failure hours later.

### Declaring a sensitive field requires consent

Same posture as actions in [ADR-0015](0015-plugin-actions-and-events.md) and plugin UI in [ADR-0020](0020-plugin-ui.md): denied by default, enabled by a deliberate act, and the prompt names which credentials this plugin will receive.

Storage encryption is irrelevant to this risk and must not be presented as mitigating it.

## Consequences

### Good

- Plugins configure themselves without Dusk knowing anything about them.
- `sensitive` as a typed field can be enforced mechanically rather than trusted to authors.
- The split keeps GitOps for everything reviewable and keeps secrets out of git entirely, without asking the operator to think about which is which.
- Write-only reads mean a compromised session or a screenshot leaks nothing, since the value cannot be retrieved after it is set.
- `ValidateConfig` turns a class of silent misconfiguration into an immediate, specific error.
- Reusing the ADR-0022 store means one encryption implementation and one rotation path.

### Bad

- **Dusk becomes a secret broker for third-party code.** Consent makes it a decision rather than a surprise, but the risk is real and unmitigated by any technical control here.
- Two schema mechanisms now exist, JSON Schema for action parameters and typed fields for configuration, and the boundary will need explaining repeatedly.
- Write-only values cannot be verified after the fact. An operator who forgets what they set has to replace it, and support conversations get harder.
- Splitting storage by sensitivity means a plugin's configuration lives in two places, and restoring an instance requires both the repository and the encrypted store.
- `ValidateConfig` gives a plugin an early, deliberate opportunity to make outbound network calls with supplied credentials, which is exactly the exfiltration path. It is a real UX gain and a real widening of the surface.
- A typed field list is less expressive than JSON Schema, so genuinely nested configuration will be awkward.

### Rejected because

- JSON Schema for configuration was rejected because it makes `sensitive` an easily missed annotation, and it drags a schema-driven form renderer into the UI for no gain on human-facing forms.
- Storing everything in the encrypted store was rejected because it removes plugin configuration from review and from GitOps, which is most of why the config repository exists.
- Storing everything in markdown was rejected because it puts credentials in git.
