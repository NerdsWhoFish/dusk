# MCP

The MCP surface is how agents read the catalog.
It is the product's main interface rather than an integration, because agents are the primary consumer.

Settled in [ADR-0010](../adr/0010-mcp-surface.md) and [ADR-0014](../adr/0014-agent-context-injection.md), implemented in `internal/mcp`.

## Connecting

Dusk serves streamable HTTP at `/mcp` on the private host.

```json
{
  "mcpServers": {
    "dusk": {
      "type": "http",
      "url": "https://dusk.example.com/mcp",
      "headers": { "Authorization": "Bearer YOUR_TOKEN" }
    }
  }
}
```

## Who may read the catalog

**The surface is off until something says.** One read returns the whole catalog, so Dusk will not pick a default:

| Setting | Effect |
| --- | --- |
| `DUSK_MCP_TOKEN` | Require that bearer token. Compared in constant time |
| `DUSK_TRUSTED_NETWORK=true` | Serve it unauthenticated |
| Neither | `/mcp` answers 503 explaining how to turn it on |

Setting both is an error rather than the stricter of the two, because two answers to "who may read this" is an unanswered question.

Whichever applies is written to the boot log, and the unauthenticated mode warns on every start.
[ADR-0012](../adr/0012-viewing-auth.md) permits a trusted-network mode for LAN and single-operator deployments and requires it to be explicit, which is why it cannot be arrived at by accident.

## The tools, each fat

The tool list is spent on every session before any work happens, so its size is a product constraint.
One tool per schema operation would produce thirty tools and cost a dozen calls to answer one question.

| Tool | What it answers |
| --- | --- |
| `search(query, kind?, limit?)` | "Where is the thing called X" |
| `get(ref)` | Everything about one entity, including its connections |
| `neighbors(ref, depth?)` | "What breaks if this goes away" |
| `changes()` | What Dusk last read from git, per repository |
| `drift(undeclared)` | What the catalog claims and reality does not support. `undeclared` adds what is running and written down nowhere |
| `dusk_context(directory?)` | The operator's estate and what they pinned worth knowing, tailored to the repository being worked in |
| `invoke(ref?, action, params?, proof?, confirm?, preview?)` | Do something to an entity, from what `get` said could be done |
| `configure(plugin, settings?, instance?)` | Read or set a plugin's non-sensitive configuration |
| `declare(ref, proof, …)` | Create or update an entity, which becomes a commit |
| `note(kind?, body?, refs?, status?, ref?, id?, proof?)` | Read or record a gotcha, a runbook, an idea, a decision |
| `kinds(namespace?, mint?, role?, aliases?, proof?)` | Read the vocabulary of kinds, or extend it |
| `page(body?, proof?)` | Read or rewrite the homepage |

`get` is deliberately fat.
An agent asking about an entity wants the whole picture, so it gets the description, attributes, relations, provenance, the notes attached to it, **and what can be done to it**, in one call rather than five.
Notes come back whole rather than as ids to fetch, because a gotcha an agent has to spend another call on is a gotcha it will not read.

## Installing a plugin adds no tools

A plugin's capabilities are [actions](../adr/0015-plugin-actions-and-events.md), not tools.
Discovery folds into `get`, because what can be done to a thing is part of the picture of that thing, and running one is `invoke` ([ADR-0041](../adr/0041-plugins-reach-agents-as-actions.md)).

The surface is therefore constant: a tenth plugin costs nothing, and an agent that has read an entity already knows what it can do to it.

An action declares a class. Read-only needs nothing; mutating needs the proof token from the read it names; **destructive needs `confirm`**, and the refusal carries the preview, or says there is none. `preview` says what would happen without doing it.

The cost is real and worth stating: an agent that never calls `get` never discovers that anything is possible.

## Reading and writing notes

`note` does both, for the same reason `page` does: the read is what yields the proof token the write needs, so a separate read tool would look optional.

Passing no body asks what is there, narrowed by `kind`, `status` and `ref`.
Passing a body writes one.

**An `id` on its own reads that one note** and returns the token to replace it, because that is the call a refused write against a note names and an instruction that does not work is worse than none.
An `id` with something to change is a replacement, as it always was.

An **idea** is a note of kind `idea`: something worth keeping that is not a description of anything.
It may attach to nothing at all, because an idea is often not about anything in the catalog yet.
It carries a status, and `status: done` or `status: dropped` closes it.

A closed note still comes back when asked for, because "I already had that idea and dropped it" is the answer somebody most needs and least expects.

A note's **kind is its rank**, so a gotcha comes out above a todo with nobody pinning it, and a todo never outranks anything in a search.
That is [`docs/kinds.md`](kinds.md).

## Reading and extending the vocabulary

`kinds` is one tool for both halves, like `note` and `page`, and for a stronger reason.
The proof token a mint presents is proof of having read the vocabulary being extended, so the "did you mean `service`" warning is delivered by the call that authorizes the mint.

Kinds are open, so declaring one that is not listed always works and nothing refuses it.
What minting adds is the two things counting cannot derive: a **role**, which every surface acts on, and **aliases**, which are the only way anything learns that `svc` means `service`.

Minting a kind the vocabulary already has, spelled the same way, **corrects** it: the role most worth fixing is the one nobody chose, because a kind nobody minted carries the default ([ADR-0054](../adr/0054-correcting-a-kinds-role.md)).
A name that is another *spelling* of one that exists is still the one refusal, and the alias it tells you to add is now a call that works.

Full rules, roles and their consequences: [`docs/kinds.md`](kinds.md).

## What `dusk_context` spends its budget on

The context has a hard byte ceiling, because every session pays for it whether or not it ever touches Dusk ([ADR-0014](../adr/0014-agent-context-injection.md)).
It carries four sections, and pinning is how something earns a place in the two that matter most.

| Section | What it is |
| --- | --- |
| Pinned, about this repository | Notes somebody pinned that attach to something this repository declares |
| What this repository declares | The refs it owns |
| Pinned, across the estate | Everything else pinned |
| What this operator has | Every ref, grouped by kind |

Sections are **paid for in priority order and printed in reading order**, so a pinned note outranks an inventory printed above it ([ADR-0050](../adr/0050-what-the-context-budget-buys-first.md)).
Written knowledge wins that contest because a ref left out is one `search` away and a gotcha left out is reachable by nothing.

Relevance orders the pinned set and does not widen it.
An unpinned note about this repository still does not appear, because pinning is the operator saying it is worth every future session.

**Nothing is dropped without being named.**
A note that will not fit whole is printed as its kind, its id and its opening line, which is what to pass back to `note`.
A kind whose refs will not fit prints its count.
Below either, a line says how many entries were left out and which tool answers them.

## Three rules the answers follow

**Every answer carries refs that feed back into `get`.**
Composability over completeness: a search result is not a dead end, it is the input to the next call.

**Answers are markdown, not nested JSON.**
Agents reason better over prose than over deeply nested objects.

**Absence is explained, never silent.**
Searching for something nobody has declared says so, and points at `changes`.
An agent that cannot tell "not in the catalog" from "the catalog is empty" will invent the difference.

## What `changes` is for

Reconcile status is the difference between a stale answer and a missing one.

It reports, per repository, the commit last read and how many entities came from it, and separates three states that look alike from the outside: repositories that declared entities, repositories that failed, and repositories with no `dusk.md` at all.

The last of those is the common case and is not a failure.

## Scope

Queries run against the **default view**: every repository at its own default branch.

Repositories disagree about what that branch is called, so there is no single git ref meaning "the catalog as it stands", and the index keeps a record of each repository's default instead.
See [storage](storage.md).

Pull request previews render a specific ref and are a UI concern, so the MCP tools do not take one.

## Writing

**Every write presents a proof token issued by a read**, so an agent cannot write what it has not looked at ([ADR-0009](../adr/0009-proof-tokens.md)).

Every read therefore returns one unasked, because read-before-write is an unusual contract and an agent that has to discover it will flail instead:

```text
Proof token `4QK7…`. Pass it to `declare` to write any of the above.
```

A token carries the version of everything its read returned, so one `search` authorizes a session's worth of writes, and any of them moving invalidates it.
Creating needs a token from **the read that could have found it and did not**, so an agent cannot duplicate something it never looked for.
For an entity that is `search`, because nothing else enumerates.
For the homepage it is `page`, which looks at the one file being created, since a search cannot name a file at all.

A refused write is an answer rather than an error, naming the call that fixes it:

```text
The write was not made.

E_PROOF_STALE: service:home/jellyfin: it changed after the read that
issued this token; call get("service:home/jellyfin")
```

**The call it names is the read of whatever was being written**, which is a separate fact from the ref rather than something derived from it.
`get` takes an entity ref, `note` takes the path that is a note's id, and `kinds` and `page` take nothing at all, so a rejection against a note names `note(id: "…")` and one against the vocabulary names `kinds()`.
An error naming a call that does not work is worse than no error: an agent follows it, fails again, and concludes the tool is broken.

### Where a write can reach

Two limits, neither of them configuration:

**Only repositories Dusk already reconciles.** A slug no sweep has seen resolves to nothing, so the set of writable repositories is bounded by what the installation granted.

**Only repositories that already contain a `dusk.md`.** Dusk never creates one. That file is how a repository consents ([ADR-0004](../adr/0004-dusk-md-convention.md)), so creating it would be Dusk granting itself permission. A human opts a repository in.

Creating an entity means adding a file, and a file no `include` glob reaches would be committed and never read. `declare` refuses that up front rather than succeeding while changing nothing.

### What lands

Write mode commits straight to the default branch, one commit per call, and the answer carries the commit URL so an agent can hand a human a link rather than claiming success.

Only the frontmatter is rewritten. The prose is left byte-identical, so a write can never disturb what somebody wrote.

### What comes back when nothing lands

**Dusk commits in write mode and nowhere else**, because that is the only mode whose App was granted `contents: write`.

In read or proposal mode a write is not refused: it comes back as the change it would have made, naming the repository, the path, and the diff ([ADR-0052](../adr/0052-a-write-that-cannot-land.md)).

````text
Nothing was committed. Dusk is in read mode, where it never writes to your repositories.

This is what it would have changed, at `services/jellyfin/dusk.md` in example/homelab:

```diff
--- a/services/jellyfin/dusk.md
+++ b/services/jellyfin/dusk.md
@@ -1,5 +1,5 @@
-title: Jellyfin
+title: Jellyfin Media Server
```
````

The diff is unified and prefixed `a/` and `b/`, so `git apply` takes it as it stands from the root of that repository.
An agent that cannot write can still hand a person the change, which is the same courtesy as handing them a link.

A proposal still needs its proof token. The diff is computed against the file as it stands, so a token from a stale read would produce one that no longer applies.

### Notes go somewhere else

A note is knowledge with no natural home, so it does not go where an entity would.
`note` writes to the **config repository**, named by `DUSK_CONFIG_REPOSITORY` as `owner/name`, in its `.dusk/` directory ([ADR-0031](../adr/0031-notes-are-files.md)).

That directory is always in scope, so a note written there is read back on the next reconcile without anything being added to an `include`.

The config repository is not exempt from consent: it needs its own root `dusk.md`, and Dusk will not create one.
Without it a note would be committed and then never read back, so `note` refuses up front instead of succeeding into a hole.

**A note's id is its path.**
The answer hands it back, and passing it as `id` reads that note, or replaces it when something is passed to change.
Replacing needs a proof token, exactly as an entity update does; writing a new note does not, because a create cannot overwrite anything.

An update merges over what the file already says, so changing a body leaves the refs and kind alone.

### Writing the same note twice

An agent with no memory of a previous session cannot know an id, so two more things stop a duplicate ([ADR-0053](../adr/0053-note-dedup.md)).

**The same words are the same note.** The path is the body's hash, so writing it again commits nothing and answers with the id of the note that already says it:

```text
That note is already there, word for word, so nothing was written.

It is at `.dusk/gotcha-1a2b3c4d.md` in example/config.
```

Nothing is merged into that note. Attaching it to something else is a `note` call with its `id` and a proof token, because a create is exempt from the gate only on the grounds that it cannot overwrite anything.

**Nearly the same words are written, and named.** A note that overlaps an existing one is committed, and the answer says what it nearly repeats:

```text
Wrote the note at `.dusk/gotcha-5e6f7a8b.md` in example/config.
...

**One note already says something close to this:**

- `.dusk/gotcha-1a2b3c4d.md` (gotcha) Transcoding is off on purpose...
```

It is a warning rather than a refusal, for the reason [ADR-0033](../adr/0033-graph-integrity.md) gives about a ref that resolves to nothing: two notes that overlap are often both worth keeping, and only a person can tell.

With no `DUSK_CONFIG_REPOSITORY` set, the tool is not offered at all.
Everything else keeps working: a tool that always fails is worse than one that is absent.

### Curating the homepage

The homepage is a file in the config repository, so an agent curates it the same way it writes anything else.
`page` is both halves of that: omit `body` and it returns the current page plus a proof token, pass `body` and it replaces the page.

One tool rather than two, because reading is what issues the token, and a separate `readPage` would look optional.
Reading is not optional here for a reason beyond the contract: **what you send becomes the whole page**, blocks are never merged, so replacing a layout without having seen it is how a homepage silently loses half of itself.

Reading a page nobody has declared returns the default written out, rather than nothing.
An agent asked to add a block to a default homepage should be editing that page, not inventing one.

Dusk parses the page **before** committing.
A page that would not render is refused with the reason, so a bad block is a failed call rather than a blank homepage discovered later.

What the blocks mean is [docs/pages.md](pages.md); the short version is that a block is a query rather than a widget, which is what makes a layout something an agent is good at tuning ([ADR-0013](../adr/0013-layout-and-pages.md)).

## Not built yet

**The other write tools.** `relate` and `push` are not built. `push` is meaningful only in proposal mode.

**Proposal mode.** The per-session branch and the pull request are not built. A write in proposal mode returns the proposed diff, the same as read mode, which is the honest answer for a mode that was never granted `contents: write` ([ADR-0010](../adr/0010-mcp-surface.md), [ADR-0052](../adr/0052-a-write-that-cannot-land.md)).

**Who ran something.** An event records an actor, and over MCP that actor is always `agent`: the surface authenticates with one shared bearer token and has no per-caller identity to record. Two agents holding one token are indistinguishable in the log.

**Proof for a plugin-scoped action.** An action that is not about one entity has nothing to have read, so the token proves the caller read the catalog rather than the thing. There is no thing yet: that is what the action is for.

**Authorization derived from repository access.** A bearer token answers "may you read", not "what may you read": it grants the whole catalog. [ADR-0012](../adr/0012-viewing-auth.md) decides that authorization should be *derived* from what GitHub says a viewer can see, which for an agent means presenting a GitHub token and having Dusk filter to the repositories it can read. The index is already partitioned by repository, so that filter is a predicate rather than a permission model. It earns its keep when a second person exists, and not before. The browser surface already derives one, and the reads that cannot be narrowed afterwards take it as an argument ([ADR-0051](../adr/0051-a-count-is-of-what-the-viewer-can-see.md)), so `mcp.Server.viewer` is the single place this becomes real: it answers `Unrestricted` today because there is no per-caller credential to derive anything from.
