# 31. A note is its own file, discovered like any other

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

[ADR-0007](0007-entity-schema.md) makes `Note` a first-class type: the "how do I do this again" unit, and the half of the product Backstage has no answer for.
[ADR-0010](0010-mcp-surface.md) says notes are what agents write most.
[ADR-0026](0026-dusk-md-schema.md) then declined to give them a home, on the grounds that a note is a markdown body with its own kind and lifetime, and cramming markdown bodies into a YAML list to ship sooner would be the wrong shape made permanent.

That deferral is now the thing blocking the write path's other half, so it has to be answered.

Three properties of notes drive the answer, and they are in tension with the file convention already chosen.

**Notes are written constantly, by agents, concurrently.**
Whatever holds them is the most contended file in the catalog.

**A note attaches to more than one entity.**
`Note.refs` is repeated in the schema, because "how the backup job reaches the NAS" is about the job and the NAS both.

**A note needs a stable id**, returned on write and included in reads, so an agent updates rather than duplicates.

Against that sits [ADR-0026](0026-dusk-md-schema.md): one file declares one entity, and its prose is that entity's description.
A note is neither an entity nor a description, so it does not fit the shape that already exists.

## Considered Options

1. **Sections in the entity's prose**, recognised by heading, such as `## Gotcha: transcoding is off`.
2. **A list in the entity's frontmatter**, each item carrying a body string.
3. **One notes file per repository**, entries separated within it.
4. **One file per note**, discovered by the same `include` the catalog already uses.

## Decision Outcome

Chosen: **option 4**.

This is not a new shape, it is the house one.
[DESIGN.md](../DESIGN.md) already holds that everything catalog-shaped is "a markdown file whose frontmatter carries the settings and whose prose explains what the thing is and why it exists", and lists a source, a plugin's configuration, a layout and a theme as members of that family.
A note belongs in it, and choosing anything else would have made notes the one catalog concept that is not written the way every other one is.

A note is a file whose frontmatter carries a `note` kind and the refs it attaches to, and whose prose is the note.

```markdown
---
dusk: v1alpha1
note: gotcha
refs:
  - service:home/jellyfin
  - host:home/nas
---

Transcoding is off on purpose. Anything that will not direct play is a client
problem, not a server one.
```

### A written note lands in the config repository

Dusk writes notes to the **config repository**, in its `.dusk/` directory.

The config repository is not invented here.
[DESIGN.md](../DESIGN.md) already holds that it "contains markdown and nothing else", [ADR-0013](0013-layout-and-pages.md) gives it portal pages, [ADR-0023](0023-plugin-configuration.md) gives it plugin configuration, and [ADR-0014](0014-agent-context-injection.md) has agent context customised by editing it.
Notes join a list of things that already belong to it.

**The deciding argument is entities with no repository.**
A host, a cluster, a physical box, and everything an ingester emits have no repository at all: [ADR-0012](0012-viewing-auth.md) says so plainly when it gives ingester-emitted entities no natural access control.
Under a satellite-only rule, a note about a NAS would have to be filed in some unrelated repository chosen at random.
For a homelab that is not a corner case, it is a large share of what the catalog describes.

### `.dusk/` is always in scope, wherever it appears

Any repository's `.dusk/` directory is read whenever it exists, with no `include` needed.

This does not weaken [ADR-0004](0004-dusk-md-convention.md)'s consent.
The root `dusk.md` is still what opts a repository in, and a directory whose entire purpose is Dusk content is consent by construction rather than by crawling: nothing is read that somebody did not put there for this.

It also closes a hole the write path would otherwise have.
A new file is only read if something reaches it, and a repository whose root declares no `include` reaches nothing, so without a default location a write would be refusable in exactly the repositories most likely to be tried first.

A note may still live in a satellite repository, either in its `.dusk/` or anywhere its `include` reaches, for the case where the knowledge genuinely belongs beside the code: a deployment runbook for that service, rather than a fact about the world.

**Entities never default anywhere.**
An entity is about something in its repository and [ADR-0001](0001-git-as-source-of-truth.md) wants its metadata beside what it documents, so creating one still requires an `include` saying where it belongs.
A note is knowledge with no natural location, which is why it gets a default and an entity does not.

### Discovery is otherwise the mechanism that already exists

Note files are also reached by the root's `include`, exactly like entity files, so a note that belongs beside the documentation it came from can live there instead.

A file declares which it is by a **required discriminator**: `note` makes it a note, `kind` and `name` make it an entity.
Both or neither is an error, because a file whose type has to be guessed is one whose type will eventually be guessed wrong.

### The path is the id

A note's id is its path in the repository.

Ids have to be stable so an agent can update rather than duplicate, and a path is already unique, already stable, already meaningful to a human reading a diff, and already the thing a write has to know.
An id field would be a second name for the same thing, kept in sync by hope.

### What this does not do

**Prose written as `## Gotchas` in an entity's description stays prose.**
It is not a note, gets no kind, and takes no part in the ranking notes are ranked by.
This is the option's real cost and it is discussed below rather than hidden.

## Consequences

### Good

- The most contended thing in the catalog is one file per note, so two agents writing two notes never touch the same file. This is the same property one-file-one-entity bought for entities, extended to the thing written far more often.
- Attaching to several entities is a list, not a duplication. The alternative would have been the same note copied into two prose bodies, drifting apart from the moment it was written.
- Ids are free and stable, and a write already needs the path, so nothing has to be looked up to update a note.
- A note can be as long as it needs to be. It is markdown in a file, not a string in a YAML value.
- Notes and entities share one discovery mechanism, one parser shape, and one set of rules about escaping the repository.

### Bad

- **There are now two ways to write a gotcha, and the better one is less obvious.** A `## Gotchas` heading in an entity's prose is how people already write, and it will keep happening; those get found by search but never ranked as gotchas. The convention that emerges naturally is the one this decision does not reward, which is the wrong way round.
- A repository with many notes accumulates many small files, and nothing here organises them beyond whatever glob the author chose.
- The id being a path means moving or renaming a note changes its id, so a rename reads as a delete plus a create.
- Requiring a discriminator makes the format slightly more ceremonious for the common case of a repository with one entity and no notes.
- Notes reference entities by ref with nothing checking those refs resolve, so a typo produces a note attached to nothing, silently. Relations have the same weakness and for the same reason: the target may legitimately live in another repository.
- Writing to the config repository loses locality by default. A note about a service no longer sits with that service, and [ADR-0004](0004-dusk-md-convention.md)'s property that a repository's own owners review changes to its own catalog entry does not hold for notes. Both cost close to nothing for [ADR-0027](0027-design-target.md)'s single operator and would cost a team more.
- Dusk now has to be told which repository is the config repository, which it has never needed before. [ADR-0013](0013-layout-and-pages.md) and [ADR-0023](0023-plugin-configuration.md) already assume one exists, so this is a debt being paid rather than taken on, but it is a new thing to configure before a note can be written at all.

### Rejected because

- **Option 1** was rejected on ids and contention, not on taste. Recognising `## Gotcha: …` would read beautifully and match how people already write, but a heading is not a stable id, so rewording one silently creates a second note; a note in an entity's prose cannot attach to a second entity; and every note write becomes a write to the entity file, which is precisely the contention [ADR-0026](0026-dusk-md-schema.md) avoided for entities and which matters more here, because notes are written more often.
- **Option 2** was rejected by [ADR-0026](0026-dusk-md-schema.md) before this ADR existed, and independently by [DESIGN.md](../DESIGN.md)'s reason for preferring markdown at all: "agents handle markdown better than they handle bare YAML". Putting the part agents write most into a YAML string inverts that. Markdown bodies inside YAML values are also unreadable in a diff, escape badly, and make a file's most valuable content its least legible.
- **Option 3** was rejected because one file per repository is the contention problem in its purest form. Every agent writing any note edits the same file, and the resulting merge conflicts land on the operator.
