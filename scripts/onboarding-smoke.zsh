#!/usr/bin/env zsh

set -euo pipefail

image=${1:-ghcr.io/nerdswhofish/dusk:latest}
root=${0:A:h:h}
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
[[ -n "$key" ]] || { print -u2 "dusk genkey returned nothing"; exit 1; }

starter=$(docker run --rm --volume "$root/examples/starter:/catalog:ro" "$image" validate /catalog)
[[ "$starter" == *"1 entities, 0 relations, 0 notes"* ]] || {
  print -u2 -- "$starter"
  print -u2 "starter catalog did not validate as one useful result"
  exit 1
}

homelab=$(docker run --rm --volume "$root/examples/homelab:/catalog:ro" "$image" validate /catalog)
[[ "$homelab" == *"5 entities, 5 relations, 1 notes"* ]] || {
  print -u2 -- "$homelab"
  print -u2 "example homelab did not validate with its complete graph"
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
  if curl --fail --silent --show-error "http://127.0.0.1:${port}/healthz" >/dev/null; then
    break
  fi
  if (( attempt == 30 )); then
    docker logs "$run_id"
    print -u2 "Dusk did not become healthy"
    exit 1
  fi
  sleep 1
done

ready=$(curl --fail --silent --show-error "http://127.0.0.1:${port}/readyz")
[[ "$ready" == *"not onboarded"* ]] || { print -u2 -- "$ready"; exit 1; }

setup=$(curl --fail --silent --show-error "http://127.0.0.1:${port}/setup")
[[ "$setup" == *"internal developer platform for homelabbers"* ]] || {
  print -u2 "setup page did not explain the product"
  exit 1
}
[[ "$setup" == *"/setup/installed?state="* ]] || {
  print -u2 "setup manifest did not include the post-install return"
  exit 1
}

print "onboarding smoke passed for $image"
