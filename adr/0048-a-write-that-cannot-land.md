# 48. A write Dusk may not commit comes back as the change it would have made

Date: 2026-08-13

## Status

Accepted

## Context and Problem Statement

[ADR-0005](0005-github-app-and-access-modes.md) gives Dusk three access modes and says why plainly: a design that treats read-only as a degraded mode will be adopted read-only anyway and will feel broken.
[ADR-0010](0010-mcp-surface.md) turns that into a rule, its fourth: in read mode, writes return the proposed diff instead of failing.

Nothing implemented it.
The write path never consulted the mode at all: `declare` routed to the file, rewrote the frontmatter, rendered it, asked GitHub to commit it, and got a 403.
Every piece of work needed to say *what the change was* had already been done, and was thrown away to return an HTTP error code.

Two facts make this worse than a missing nicety.

**The permissions really are per mode.** `permissionsFor` grants `contents: write` only in write mode, so GitHub is the thing enforcing read-only, not Dusk. Anything Dusk decides here is about being useful, not about being safe.

**Proposal mode is the default at setup, and it has never been able to write.** It is granted `pull_requests: write` and `contents: read`, so a commit from it fails exactly as it does in read mode. That is not a feature deferred until somebody needs it; it is the mode most deployments land in, in which every write has always failed.

## Considered Options

1. **Leave it.** The App's permissions refuse the commit and the operator reads a 403.
2. **Refuse early**, with a message naming the mode, and no diff.
3. **Return the change as a unified diff**, from every write path, whenever Dusk was not granted a commit.
4. **Build proposal mode now**: a branch and a pull request there, a diff in read mode.
5. **Try the commit and turn a 403 into a diff**, with no notion of a mode anywhere in the write path.

## Decision Outcome

Chosen: **option 3**. Dusk commits in write mode; every other mode answers with the diff it would have committed, against the repository and path it would have committed it to.

### One place decides, so no write path can forget

Four calls write a file: an entity update, an entity create, a note, and the homepage.
All four now end at `land`, which asks what Dusk was granted and either commits or returns the diff.

That is the whole reason it is one function.
A rule spread across four call sites is a rule the fifth write path will not have, and it would fail as a 403 the way all of them used to.

### A diff, because it is the one form everything already reads

The alternatives for "what would have happened" were the rendered file whole, and the semantic diff `index.Compare` already produces for pull request previews.
The whole file costs context on every call and buries a one-line change in a hundred unchanged ones.
The semantic diff describes the *catalog* rather than the file, so it reads well and cannot be applied to anything.

A unified diff is applicable by `git apply`, readable by a person, and shows the change in place.
It is rendered by `pkg/textdiff` rather than a dependency: nothing in the module graph provides one today, and taking a dependency to print a few lines of text is a trade this repository has consistently refused.

### The proof gate still applies to a proposal

A proposal is still a write an agent asked for, so [ADR-0009](0009-proof-tokens.md)'s gate runs first and read mode is not the way around it.
The reason is not symmetry: the diff is computed against the file as it stands, so a token from a stale read would produce a diff that no longer applies.

### Proposal mode gets the same answer, and this does not build proposal mode

Proposal mode was never able to commit, so returning the diff replaces a 403 with something usable and takes nothing away.
The per-session branch, the pull request, and the sweep for abandoned branches that [ADR-0010](0010-mcp-surface.md) describes are all still unbuilt, and `docs/status.md` still says so.

When they are built, `land` gains a branch and the answer becomes a pull request URL.
The callers do not change, which is the point of there being one of them.

### The browser is told the same thing

Closing a note is the one write the HTTP API offers, and it now answers with the proposal rather than a success the note did not have.
The note stays open in the UI and the diff is shown beside it.

## Consequences

### Good

- Read-only is a posture rather than a wall. An agent in read mode does the work and hands a person something to apply, which is [ADR-0005](0005-github-app-and-access-modes.md)'s stated intent.
- Proposal mode goes from "every write fails" to "every write answers", without pre-empting the decision about how proposal mode should eventually work.
- One gate covers four write paths, so the rule cannot be half-applied.
- `pkg/textdiff` is small, reusable, and tested on its own, so the hardest part to get right is not entangled with the write path.

### Bad

- **Dusk now has an opinion about what GitHub would allow.** It comes from the mode recorded at onboarding, not from the App's live permissions, and those can disagree: an operator who later grants `contents: write` on GitHub still gets proposals, because nothing reconciles the two. Option 5 is the only design that cannot drift this way, and it was rejected for a worse reason.
- **A Writer with no `Access` commits.** That is deliberate, because the App's permissions are the real gate, but it means a wiring mistake shows up as the 403 this ADR exists to remove rather than as something loud.
- A refused write costs the same API reads as one that lands. Being refused was never free; it is only useful now.
- The diff is computed against the default branch as it was read, so a race makes it a diff that no longer applies cleanly. The proof token narrows the window and does not close it.
- **Nothing remembers a proposal.** It is returned and gone. An agent that ends its session without relaying the diff has lost the work, and events are a bounded in-memory buffer that this does not go into.
- This repository now maintains a diff implementation. It is line based with a cap past which it degrades to replacing the file wholesale, and it will never be as good as git's.

### Rejected because

- **Option 1** was rejected because it throws away work already done and answers with a GitHub status code, which is the broken-feeling degradation both [ADR-0005](0005-github-app-and-access-modes.md) and [ADR-0010](0010-mcp-surface.md) exist to prevent.
- **Option 2** was rejected because a clear refusal is still a refusal. The agent is holding the change; withholding it helps nobody.
- **Option 4** was rejected as a larger decision wearing this one's clothes. Proposal mode needs a branch per session and a pull request lifecycle, and choosing that shape now, to fix an error message, is how a deferred decision gets made by accident.
- **Option 5** was rejected on what a 403 means. GitHub answers 403 for a secondary rate limit as well as for a permission it did not grant, so a busy sweep would silently turn a real write into a proposal and the operator would conclude Dusk was in read mode. It also spends a request to be told no on every write. It is the only option that cannot disagree with GitHub, and that is worth remembering if the drift above ever bites.
