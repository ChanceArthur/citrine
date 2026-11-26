#!/bin/sh
set -eu

cd "$(dirname "$0")/../.."
IMAGE=${IMAGE:-ghcr.io/chancearthur/citrine}

podman build --pull=newer \
  --build-arg VERSION="${VERSION:-dev}" \
  --build-arg COMMIT="${COMMIT:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}" \
  --build-arg SOURCE="${SOURCE:-}" \
  --annotation org.opencontainers.image.created="$(date -u +%FT%TZ)" \
  -f Containerfile -t "$IMAGE:latest" .

[ "${1:-}" != push ] || podman push "$IMAGE:latest"
