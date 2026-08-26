# Getting started

Dusk is one self-hosted service for one trusted homelabber and their agents.
It needs a persistent data directory, an encryption key, a browser-reachable URL, outbound access to GitHub, and a repository containing `dusk.md`.

## Container quickstart

Export the URL that will reach Dusk through your reverse proxy, then generate two independent secrets with the published image.

```zsh
git clone https://github.com/NerdsWhoFish/dusk.git
cd dusk
export DUSK_PRIVATE_HOST=https://dusk.example.com
export DUSK_ENCRYPTION_KEY="$(docker run --rm ghcr.io/nerdswhofish/dusk:latest genkey)"
export DUSK_MCP_TOKEN="$(docker run --rm ghcr.io/nerdswhofish/dusk:latest genkey)"
```

Start the service with the included Compose file.

```zsh
docker compose --file deploy/compose.yaml up --detach
```

The default bind is `127.0.0.1:8080`, which is appropriate behind a reverse proxy on the same machine.
Set `DUSK_BIND=0.0.0.0:8080` only when the LAN should reach Dusk directly.
The named volume is created with the ownership the non-root image needs.

Open `$DUSK_PRIVATE_HOST/setup` and complete the GitHub flow described below.

## GitHub App setup

The setup page asks for an access mode and an optional organization owner.

- `Read only` reads catalog files and returns proposed changes as diffs.
- `Propose changes` reads catalog files and opens pull requests for writes.
- `Write directly` commits catalog changes to the owning repository.

Choose the smallest mode that matches how you want agents to work.
GitHub shows the exact permissions before it creates the App.
Dusk exchanges the one-hour manifest code, stores the generated private key and webhook secret encrypted, and then sends you to the App installation screen.
Select only the repositories that should participate in the catalog.

After installation, GitHub returns to Dusk and Dusk starts an initial sweep.
The return carries an unguessable one-use state because GitHub's `installation_id` query parameter is not proof that GitHub sent the request.

If a selected repository already has a root `dusk.md`, it appears without additional configuration.
Otherwise copy [`examples/starter/dusk.md`](../examples/starter/dusk.md) to the root of one selected repository and push it.
The post-install page contains the same starter, so the first useful search result does not require finding this guide again.

The fuller [`examples/homelab`](../examples/homelab) shows five related entities and an attached runbook.
Validate either example, or any checkout, with the image that will run it.

```zsh
docker run --rm \
  --volume "$PWD/examples/homelab:/catalog:ro" \
  ghcr.io/nerdswhofish/dusk:latest validate /catalog
```

## URLs and network access

`DUSK_PRIVATE_HOST` is where a browser reaches the UI and where GitHub returns that browser during App registration and installation.
It may be private because the redirect is followed by the operator's browser.

`DUSK_PUBLIC_HOST` is where GitHub sends `POST /webhooks` when that differs from the private host.
Expose only `/webhooks` publicly when the UI should remain private.
Leave it unset when one hostname serves both.

Webhooks are the fast path, not the correctness floor.
A deployment with no GitHub-reachable endpoint still works through the periodic sweep, and the post-install return starts the first sweep immediately.
GitHub cannot deliver webhooks to `localhost` or `127.0.0.1`, so a local-only deployment should expect failed deliveries and rely on polling.

| Direction | Destination | Required for |
| --- | --- | --- |
| Browser to Dusk | `DUSK_PRIVATE_HOST` | Setup, UI, and GitHub callbacks |
| Agent to Dusk | `DUSK_PRIVATE_HOST/mcp` | Catalog reads, notes, and actions |
| GitHub to Dusk | `DUSK_PUBLIC_HOST/webhooks` | Immediate reconciliation; optional |
| Dusk to `api.github.com` | HTTPS 443 | App setup, repository reads and writes, and plugin discovery |
| Dusk to `github.com` and GitHub release asset hosts | HTTPS 443 | App registration and plugin installation |
| Container runtime to `ghcr.io` | HTTPS 443 | Pulling Dusk and Helm artifacts |

The GitHub App needs access only to repositories selected during installation.
`DUSK_ALLOWED_ACCOUNTS` narrows which account installations Dusk accepts and defaults to the account that owns the App.
`DUSK_PLUGIN_ORGS` is a separate code-trust boundary because installed plugins run with Dusk's process permissions.

## Agent access

Set exactly one agent access mode.

- `DUSK_MCP_TOKEN` requires a bearer token and is the normal choice.
- `DUSK_TRUSTED_NETWORK=true` removes authentication from `/mcp`, including mutations, and is appropriate only when every host that can reach Dusk is trusted as the operator.

The browser accepts the same token at `/login`.
An MCP client connects to `$DUSK_PRIVATE_HOST/mcp` and sends `Authorization: Bearer $DUSK_MCP_TOKEN`.
See [the MCP guide](mcp.md) for client configuration and the complete tool contract.

## Helm install

Create a Secret outside Helm's command-line values so the encryption key and MCP token do not land in shell history or the Helm release values.

```zsh
kubectl create namespace dusk
kubectl --namespace dusk create secret generic dusk-secrets \
  --from-literal=DUSK_ENCRYPTION_KEY="$DUSK_ENCRYPTION_KEY" \
  --from-literal=DUSK_MCP_TOKEN="$DUSK_MCP_TOKEN"
```

Install the OCI chart with an explicit application URL and the Secret as environment input.

```zsh
helm install dusk oci://ghcr.io/nerdswhofish/charts/dusk \
  --namespace dusk \
  --set dusk.privateHost="$DUSK_PRIVATE_HOST" \
  --set dusk.existingSecret=dusk-secrets \
  --set ingress.enabled=true \
  --set 'ingress.hosts[0].host=dusk.example.com' \
  --set 'ingress.tls[0].secretName=dusk-tls' \
  --set 'ingress.tls[0].hosts[0]=dusk.example.com'
```

Configure `ingress.className`, TLS, storage class, and annotations for the cluster rather than copying the example blindly.
The chart intentionally keeps one replica and uses `Recreate` because SQLite and one PVC are a single-writer deployment.

## Configuration reference

| Variable | Default | Purpose |
| --- | --- | --- |
| `DUSK_PRIVATE_HOST` | required | Browser, API, MCP, and setup callback origin |
| `DUSK_PUBLIC_HOST` | private host | GitHub webhook origin when it differs |
| `DUSK_ENCRYPTION_KEY` | required | Base64 32-byte master key from `dusk genkey` |
| `DUSK_MCP_TOKEN` | off | Bearer token for people and agents |
| `DUSK_TRUSTED_NETWORK` | `false` | Explicit unauthenticated MCP mode |
| `DUSK_DATA_DIR` | `/var/lib/dusk` | Persistent credentials, action journal, plugins, and index |
| `DUSK_CONFIG_REPOSITORY` | unset | `owner/name` repository where agents write notes and portal configuration |
| `DUSK_ALLOWED_ACCOUNTS` | App owner | Comma-separated accounts whose installations may enter the catalog |
| `DUSK_PLUGIN_ORGS` | `NerdsWhoFish` | Comma-separated organizations trusted to publish executable plugins |
| `DUSK_PROOF_TTL` | `1h` | Maximum age of a read-before-write proof |
| `DUSK_MCP_SESSION_TIMEOUT` | `30m` | Idle lifetime of an MCP session |
| `DUSK_AI_BASE_URL` | unset | OpenAI-compatible API base URL; leaving it unset disables AI search |
| `DUSK_AI_API_KEY` | unset | Provider bearer token; required with the AI base URL |
| `DUSK_AI_MODELS` | unset | Comma-separated model allowlist shown in search |
| `DUSK_AI_DEFAULT_MODEL` | first allowed model | Deployment default model when AI search is enabled |
| `DUSK_EMBEDDINGS_BASE_URL` | unset | OpenAI-compatible embeddings base URL; unset keeps exact plus FTS search |
| `DUSK_EMBEDDINGS_API_KEY` | unset | Optional bearer token; local endpoints may be keyless |
| `DUSK_EMBEDDINGS_MODEL` | unset | Embedding model name; required with the embeddings base URL |
| `DUSK_EMBEDDINGS_REPAIR_INTERVAL` | `1h` | Full repair sweep; catalog writes also refresh changed documents |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset | OTLP/HTTP collector endpoint; leaving it unset disables telemetry |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` | OTLP transport protocol when telemetry is enabled |
| `OTEL_RESOURCE_ATTRIBUTES` | unset | Comma-separated OpenTelemetry resource attributes such as the deployment environment |

Run `docker run --rm ghcr.io/nerdswhofish/dusk:latest serve --help` for the boot-time reference carried by the artifact itself.

## Backup and restore

Back up the entire `DUSK_DATA_DIR` and the separate `DUSK_ENCRYPTION_KEY`.
The directory contains encrypted GitHub App credentials, the durable action journal, installed plugins and their sealed configuration, and the SQLite materialized index.
The index can be rebuilt from Git, but the other files cannot.

Stop Dusk before copying the directory or snapshot the underlying volume atomically.
Copying a live SQLite database without its WAL can produce a backup that looks complete and is not.

For Compose, stop the service and archive the named volume through a temporary container, then start it again.

```zsh
docker compose --file deploy/compose.yaml stop dusk
docker run --rm \
  --volume dusk-data:/data:ro \
  --volume "$PWD:/backup" \
  alpine tar -C /data -czf /backup/dusk-data.tgz .
docker compose --file deploy/compose.yaml start dusk
```

Set `DUSK_VOLUME` before the first start when the volume should use a name other than `dusk-data`.
Store the encryption key in a password manager or secret store rather than inside the archive it unlocks.

To restore, stop Dusk, restore the complete directory into an empty volume, supply the same encryption key, and start the same Dusk image version that created the backup.
Check `/healthz`, `/readyz`, the `changes` tool, and one known entity before upgrading anything.

For Kubernetes, use the storage system's consistent PVC snapshot mechanism while the Deployment is scaled to zero, and back up the Secret independently.
Restore the snapshot into a PVC, set `persistence.existingClaim`, restore the Secret, and then scale Dusk back up.

## Upgrade and rollback

Pin production deployments to a release tag instead of `latest`.
Read the release notes, take a backup, pull the new image or chart, and then replace the single Dusk process.

For Compose, set `DUSK_IMAGE=ghcr.io/nerdswhofish/dusk:X.Y.Z`, run `docker compose pull`, and run `docker compose up --detach`.
For Helm, set `image.tag=X.Y.Z` when upgrading an existing chart, or select a newer chart whose `appVersion` already targets that image.

Verify health, readiness, the last successful reads, one search, and one agent connection after the restart.

A rollback is the old image plus the pre-upgrade data snapshot and the same encryption key.
Do not point an older binary at a data directory already opened by a newer one, because persistent formats are forward-migrated and no backward-migration contract exists.
Restore the snapshot first, then start the previous pinned image or run `helm rollback` with the PVC restored to the matching snapshot.
