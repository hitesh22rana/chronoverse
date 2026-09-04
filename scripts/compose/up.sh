#!/bin/sh
set -eu

compose_file=${COMPOSE_FILE:-compose.dev.yaml}

docker compose -f "$compose_file" run --rm init-certs
exec docker compose -f "$compose_file" up -d "$@"
