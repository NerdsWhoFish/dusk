# 22. Credentials are encrypted at rest with a required external key

Date: 2026-08-11

## Status

Accepted

## Context and Problem Statement

Dusk holds durable secrets: the GitHub App private key, the webhook secret, and the OAuth client secret, per [ADR-0005](0005-github-app-and-access-modes.md) and [ADR-0012](0012-viewing-auth.md).
Losing them means re-onboarding. Leaking the private key means someone else can act as the App on every repository it is installed on.

The obvious approach, encrypting the credential file with a key stored beside it, is worth naming so it is not chosen by accident.
**A key stored next to its ciphertext is obfuscation, not encryption.** It defends against a casual read of the file and nothing else.

So the design question is not whether to encrypt, but where the key lives and what that combination actually defends against:

| Threat | Encryption helps |
| --- | --- |
| Stolen volume snapshot, etcd backup, disk image | Yes, if the key is not on that disk |
| Shell access inside the running pod | No. The process must decrypt to function |
| Leak via logs, API responses, errors, git | No. That is a types problem |

## Considered Options

1. **File permissions only**, `0600` in the data directory.
2. **Optional encryption**, with a warning at startup when no key is configured.
3. **Required encryption**, refusing to boot without a key.

## Decision Outcome

Chosen: **option 3**.

`DUSK_ENCRYPTION_KEY` is required.
Dusk refuses to start without it, and validates length and encoding at boot rather than failing later on first decrypt.

There is no unencrypted mode, so there are no unencrypted deployments.
A startup warning was considered and rejected: warnings are read once and ignored thereafter, and an insecure mode that exists will be run in production by somebody.

This is the one manual step before first boot, and it is accepted as such.

### The key lives outside the data directory

The key comes from the environment, which in Kubernetes means a Secret.
Ciphertext sits on the PersistentVolumeClaim, the key does not.
Those are different blast radii, which is the entire point: a stolen volume snapshot is worthless on its own.

### Envelope encryption, so rotation is possible

A random data key encrypts the credentials; the master key from the environment encrypts the data key.

Rotating the master key rewrites one small wrapped blob rather than re-encrypting everything.
Without this, a required key with no rotation path means changing it is unrecoverable, and rotation is far cheaper to build now than to retrofit.

AES-GCM throughout, with a key derivation step for the master key.

### The chart never generates the key

The Helm chart requires the key to be supplied, or points at an existing Secret. It does not invent one.

Helm's `randAlphaNum` regenerates on every `helm upgrade` unless guarded by a `lookup` against the existing Secret.
Charts get this wrong routinely, and here it would silently rotate the key on upgrade and brick the installation.
Requiring the operator to hold the key makes losing it a deliberate act rather than an upgrade side effect.

### Derived credentials are never persisted

Installation tokens live an hour and are mintable from the private key at any time, so they stay in memory.
Only the private key, webhook secret, and client secret are durable.

### Secrets are a type, not a convention

Secret values use a type whose `String`, `MarshalJSON`, and `LogValue` all render `[REDACTED]`.

Accidental exposure through a log line or an API response is by far the most likely failure, and encryption does nothing for it.
Making leakage require deliberate effort is worth more in practice than the encryption is.

## Consequences

### Good

- There is no insecure mode, so no deployment is accidentally running one.
- A stolen volume snapshot or backup is useless without the Secret, which is the threat encryption can actually address.
- One code path instead of two, with no conditional-encryption branches to get wrong.
- Envelope encryption makes rotation a small operation rather than an impossible one.
- The redacting type converts the most likely failure from a review item into something that takes effort to do.
- The credential store being an interface means Vault, SOPS, or direct Secret access are later seams rather than rewrites.

### Bad

- **Losing the key means losing the credentials and re-onboarding.** That is a real operational risk created deliberately, and it must be prominent in the chart notes and the docs rather than buried.
- First boot now has a manual step, which is friction on the exact path where a new user decides whether this is worth it.
- Refusing to boot is hostile when someone is just trying the thing locally, and no escape hatch is offered on purpose.
- Encryption protects nothing against an attacker with shell access in the pod, and that limitation must not be oversold.
- Key material in an environment variable is visible to anything that can read the process environment, which is weaker than a file descriptor or a mounted file would be.

### Rejected because

- File permissions alone were rejected because `0600` is worthless against the threat that actually matters here, which is a copied volume or backup.
- Optional encryption was rejected because it produces two code paths and, in practice, unencrypted production deployments. A warning at startup is read once and then never again.
