# 43. Plugin configuration stays out of git, and an agent sets it through a tool

Date: 2026-08-13

## Status

Accepted. Amends [ADR-0023](0023-plugin-configuration.md) and [ADR-0041](0041-plugins-reach-agents-as-actions.md).

## Context and Problem Statement

[ADR-0023](0023-plugin-configuration.md) decided that a plugin's configuration splits on sensitivity: non-sensitive values are markdown frontmatter in the config repository, sensitive values live in the encrypted store.

[ADR-0041](0041-plugins-reach-agents-as-actions.md) then decided how an agent configures a plugin, and reasoned directly from that split: "Non-sensitive configuration is *already* a file in git, so an agent configuring a plugin is an agent editing a declaration, which is `declare` doing what it does."

The premise is false.
Nothing ever wrote plugin configuration to the config repository.
Both halves live in `installed.json` beside the binary, and `sensitive` was a form hint that made the field render as a password box and nothing else, so every plugin credential sat in readable JSON on the volume and was returned by the plugins API to anything holding the token.

So there are two questions, not one.
Where does configuration live, and how does an agent change it.
ADR-0041 answered the second by assuming an answer to the first that was never built.

## Considered Options

For where it lives:

1. **Build what ADR-0023 decided**: non-sensitive configuration becomes frontmatter in the config repository, read through the reconciler.
2. **Keep it beside the binary**, and seal the sensitive half under the master key.

For how an agent changes it:

1. **Through `declare`**, as ADR-0041 said, which requires option 1 above.
2. **A `configure` tool.**
3. **Not at all**, leaving configuration a human's job at a form.

## Decision Outcome

Chosen: **configuration stays beside the binary, and an agent changes it through a `configure` tool**.

### Why not move it into git

The attraction is real: review, history, and one place where everything Dusk does is declared.
Three things weigh against it, and the first is decisive.

**A plugin must be configurable before Dusk can read anything.**
The config repository is reached over the GitHub API with credentials from onboarding, and [ADR-0042](0042-installing-plugins.md) deliberately made an installed plugin start from disk so that "an offline Dusk comes up with what it had".
Configuration in git makes a plugin's ability to start depend on a network call, an installation token, and a completed reconcile.
That converts a local, always-available fact into a distributed one, and the failure mode is a plugin that will not start because GitHub is slow.

**A local cache would be needed anyway**, so the choice is not "git or disk" but "git and disk, kept in step" against "disk".
Two copies of one fact is exactly what [`docs/packages.md`](../docs/packages.md) warns about, and the drift is silent.

**Half of it cannot go in git regardless.** A credential stays in the encrypted store either way, so the split ADR-0023 wanted for reviewability delivers reviewability of the boring half only.

### Sensitive values are sealed, which is the half of ADR-0023 that mattered

The split now exists, with the sensitive half sealed under the same master key as the App's own credentials, written beside the record so uninstalling takes the credential with it.
It reaches the plugin over its own socket and is never read back.
A field submitted empty keeps what is stored, because a write-only form submits empty when it was not retyped; an explicit null forgets it.
A credential written in the clear by an older Dusk moves on the next start.

### `configure` is a tool, and that is consistent with ADR-0041

ADR-0041's decision is that **the surface does not grow when a plugin is installed**, and its rejected options were per-plugin tools.
A single fixed `configure` tool does not grow with the marketplace, so the property that decision exists to protect is intact.
What changes is the implementation sketch, which assumed a file that does not exist.

It merges over what is there rather than replacing it: an agent setting one field should not clear the rest, which is what sending a whole form does.

**Sensitive fields are refused by name rather than accepted and dropped.**
ADR-0041's reasoning is untouched and is the reason: a secret passed as a tool argument is already in the agent's context, its transcript, and any log along the way, and encrypting it on arrival unwrites none of that.
Refusing loudly also teaches, where silently dropping produces a plugin that mysteriously does not work.

## Consequences

### Good

- A plugin starts from disk with everything it needs, so ADR-0042's offline boot survives.
- One place holds a plugin's configuration, so there is nothing to keep in step.
- The half of ADR-0023 that carried the security property is built, and a credential is no longer readable on the volume or over the API.
- ADR-0041's constant surface is preserved: one tool, however many plugins are installed.
- Merging rather than replacing makes a partial edit safe, which is the shape an agent actually uses.

### Bad

- **Plugin configuration is not reviewable and has no history.** Changing which cluster a plugin watches leaves no diff and no commit, which is a genuine loss against the GitOps posture the rest of the product holds to.
- It is not rebuildable from git, so it joins the plugin binaries as state the volume holds and a fresh install does not reproduce. ADR-0042 already weakened that guarantee; this widens the hole rather than opening a new one.
- Two ADRs now describe a storage split that only half exists, and a reader of ADR-0023 alone will believe configuration is in git. This file is the correction, and both are amended to point at it.
- An agent can change what a plugin observes with no record beyond the event log, which is in memory.

### Rejected because

- **Option 1 for storage** was rejected because it makes starting a plugin depend on the network, needs a local cache anyway, and delivers reviewability for only the non-sensitive half.
- **`declare` for the write path** was rejected because it presumes option 1. Routing `declare` to somewhere other than git would be worse: that tool's entire meaning is "commit this to the repository that owns it".
- **Leaving configuration human-only** was rejected because it makes "an agent can set this up" false for everything, not only for credentials, and the boring half is exactly what an agent should be doing.
