#!/usr/bin/env bash

set -euo pipefail

image=${1:-ghcr.io/nerdswhofish/dusk:latest}
root=$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." >/dev/null && pwd -P)
run_id="dusk-onboarding-${$}-${RANDOM}"
volume="${run_id}-data"

cleanup() {
  docker rm --force "$run_id" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if [[ ${DUSK_SMOKE_PULL:-true} == true ]]; then
  docker pull "$image" >/dev/null
fi

key=$(docker run --rm "$image" genkey)
[[ -n "$key" ]] || { printf 'dusk genkey returned nothing\n' >&2; exit 1; }

starter=$(docker run --rm --volume "$root/examples/starter:/catalog:ro" "$image" validate /catalog)
[[ "$starter" == *"1 entities, 0 relations, 0 notes"* ]] || {
  printf '%s\nstarter catalog did not validate as one useful result\n' "$starter" >&2
  exit 1
}

homelab=$(docker run --rm --volume "$root/examples/homelab:/catalog:ro" "$image" validate /catalog)
[[ "$homelab" == *"5 entities, 5 relations, 1 notes"* ]] || {
  printf '%s\nexample homelab did not validate with its complete graph\n' "$homelab" >&2
  exit 1
}

docker volume create "$volume" >/dev/null
docker run --detach --name "$run_id" \
  --publish 127.0.0.1::8080 \
  --env DUSK_PRIVATE_HOST=https://dusk.example.com \
  --env DUSK_ENCRYPTION_KEY="$key" \
  --env DUSK_MCP_TOKEN=onboarding-smoke-token \
  --volume "$volume:/var/lib/dusk" \
  "$image" >/dev/null

address=$(docker port "$run_id" 8080/tcp)
port=${address##*:}
for attempt in {1..30}; do
  if curl --fail --silent "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1; then
    break
  fi
  if (( attempt == 30 )); then
    docker logs "$run_id"
    printf 'Dusk did not become healthy\n' >&2
    exit 1
  fi
  sleep 1
done

ready=$(curl --fail --silent --show-error "http://127.0.0.1:${port}/readyz")
[[ "$ready" == *"not onboarded"* ]] || { printf '%s\n' "$ready" >&2; exit 1; }

setup=$(curl --fail --silent --show-error "http://127.0.0.1:${port}/setup")
[[ "$setup" == *"internal developer platform for homelabbers"* ]] || {
  printf 'setup page did not explain the product\n' >&2
  exit 1
}
[[ "$setup" == *"/setup/installed?state="* ]] || {
  printf 'setup manifest did not include the post-install return\n' >&2
  exit 1
}

printf 'onboarding smoke passed for %s\n' "$image"
