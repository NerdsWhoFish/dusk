# 66. A plugin update proves an immutable candidate before switching the active record

Date: 2026-08-18

## Status

Accepted

## Context and Problem Statement

[ADR-0042](0042-installing-plugins.md) decided that Dusk installs release binaries on persistent disk and applies an update only when the operator asks.
It deliberately left the updater mechanics open.

The first implementation stopped the running plugin before it downloaded the release, overwrote `<plugin>/plugin` in place, wrote `installed.json`, and then tried to start the new process.
Every fallible step therefore happened after the known-good process was gone.
A failed download caused an unnecessary outage, a torn binary or record write could make the two disagree, and a release that passed its checksum but could not answer `Describe` replaced the only version known to work.

[ADR-0055](0055-plugin-supervision.md) restarts the selected version after a crash.
It cannot repair a bad selection, because faithfully restarting the broken update is the problem rather than the remedy.

The updater needs one durable fact that can change atomically.
Two mutable files cannot provide that fact, so the question is what the active record points at and when that pointer moves.

## Considered Options

1. **Keep overwriting the live binary**, but download it before stopping the process and restore a backup when startup fails.
2. **Write a new binary and record to temporary paths**, then rename both into place.
3. **Store immutable binaries by digest and atomically replace one active record** after the candidate is ready.
4. **Delegate rollback to the process supervisor**, letting it select an earlier version after repeated failures.

## Decision Outcome

Chosen: **option 3**.

Every verified release is staged at `<plugin>/versions/<archive-sha256>/plugin`.
That path is immutable, so `installed.json` is the only active pointer and its atomic rename is the cutover.
A record from an older Dusk falls back to `<plugin>/plugin` when no versioned binary exists, so an existing installation migrates on its next update rather than requiring a flag day.

The staged process starts on a candidate socket while the active process and all of its scheduled instances remain untouched.
It must answer a conforming `Describe`, identify itself as the plugin being updated, report the release version, and serve every declared UI asset before it is considered ready.
Checksum verification proves what was downloaded; readiness proves that the verified bytes can satisfy Dusk's runtime contract.

Only then does Dusk atomically write the active record.
If staging, verification, startup, description, asset loading, version checking, or the record write fails, the candidate is stopped and the old process, record, rotation, configuration, credentials, instances, and enabled actions remain untouched.

After the pointer changes, Dusk halts the old supervision, removes the old instances from the rotation, stops the old process, and adopts the already-ready candidate.
No fallible filesystem operation remains after the pointer moves.
The supervisor then governs that selected version exactly as [ADR-0055](0055-plugin-supervision.md) specifies.

Plugin lifecycle mutations are serialized inside one Dusk process.
Install, update, configure, enable, restart, uninstall, restore, shutdown, and supervisor restarts may not publish records or processes concurrently, because each otherwise preserves a different snapshot of configuration and the last writer silently discards the other.
This is a single-operator administration path, so a global lifecycle gate is simpler and more honest than per-plugin concurrency.

Previous immutable binaries are retained.
Deleting old versions is not part of activation, because cleanup must never remove the only known-good rollback material and disk pressure is less dangerous than an updater inventing garbage collection policy during cutover.

## Consequences

### Good

- A failed update does not interrupt the plugin already serving.
- The active record never deliberately points at a partially written binary, and one atomic file decides what an offline restart executes.
- Configuration, encrypted credentials, named instances and enabled actions cross an update as one preserved record.
- A release is checked against the host contract before it becomes active, rather than after the operator has already lost the old process.
- Existing installations continue to boot from the legacy path and move to immutable storage on their next successful update.
- Concurrent lifecycle requests cannot publish stale copies of the same record.

### Bad

- Every successful update retains another plugin binary, so the persistent volume grows until a separate conservative retention policy is decided.
- An update briefly runs two trusted versions of one plugin with Dusk's permissions.
The candidate is not scheduled and receives only its configuration, but process isolation remains exactly as weak as [ADR-0042](0042-installing-plugins.md) records.
- The candidate socket is not the socket a later supervisor restart uses, so the socket path is no longer a stable process identity.
It was never part of the plugin contract; the environment variable is.
- A crash after the active record changes but before the old process stops can leave both processes alive until the host restarts.
The durable state still selects one version, but atomic disk state cannot make two operating-system processes a transaction.
- A global lifecycle gate sacrifices parallel plugin administration.
Those operations are rare and human-triggered, so avoiding lost configuration is worth more than concurrent updates.

### Rejected because

- **Option 1** was rejected because a backup and a mutable live path create a rollback protocol with several crash points.
The updater would have to infer which of the binary, backup and record is authoritative after every possible interruption.
- **Option 2** was rejected because two renames are not one atomic decision.
Whichever file moves first creates a state where the record and binary disagree, and a crash can preserve that state.
- **Option 4** was rejected because supervision answers a runtime crash, not installation validity.
It would repeatedly execute the newly selected broken version before deciding to mutate durable version history, coupling two failure policies and making the operator's explicit selection reversible by an unrelated crash counter.

## Amendments

### 2026-08-18 — A tag and GoReleaser's version are the same semantic release

The candidate still has to report the release it came from, but the comparison now treats `v1.2.3` from GitHub and `1.2.3` from GoReleaser's standard `Version` template as the same semantic version.
The first Home Assistant plugin release exposed the mismatch: its checksum and contract passed, then Dusk rejected the process because every official plugin's GoReleaser configuration injects the version without the tag prefix.
Only a valid semantic version receives this normalization, including prerelease and build suffixes; arbitrary version strings still compare exactly, and an actually different version is still refused.
Changing every plugin to re-add the prefix was rejected because it duplicates host compatibility policy across every publisher and fights the release tool's documented value without adding identity.
