#!/usr/bin/env bash

set -euo pipefail

grafana_container="${GRAFANA_CONTAINER:-lgtm}"

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

if ! docker container inspect "$grafana_container" >/dev/null 2>&1; then
  die "Grafana container '$grafana_container' does not exist"
fi

if [ "$(docker container inspect --format '{{.State.Running}}' "$grafana_container")" != "true" ]; then
  die "Grafana container '$grafana_container' is not running"
fi

otel_lgtm_mount="$(
  docker container inspect \
    --format '{{range .Mounts}}{{if eq .Destination "/otel-lgtm"}}{{.Type}}{{end}}{{end}}' \
    "$grafana_container" 2>/dev/null
)"
if [ -n "$otel_lgtm_mount" ]; then
  die "Grafana container '$grafana_container' still mounts /otel-lgtm; recreate it with the updated Compose file first"
fi

docker exec "$grafana_container" /bin/sh -ec '
  test -n "${GF_SECURITY_ADMIN_USER:-}" || {
    echo "error: GF_SECURITY_ADMIN_USER is empty in the Grafana container" >&2
    exit 1
  }
  test -n "${GF_SECURITY_ADMIN_PASSWORD:-}" || {
    echo "error: GF_SECURITY_ADMIN_PASSWORD is empty in the Grafana container" >&2
    exit 1
  }
  test -x /otel-lgtm/grafana/bin/grafana || {
    echo "error: Grafana executable is missing from the image" >&2
    exit 1
  }
  exec /otel-lgtm/grafana/bin/grafana cli \
    --homepath /otel-lgtm/grafana \
    --configOverrides cfg:default.paths.data=/data/grafana/data \
    admin reset-admin-password "$GF_SECURITY_ADMIN_PASSWORD"
'

anonymous_status="$(
  docker exec "$grafana_container" /bin/sh -ec '
    curl --silent --show-error --output /dev/null --write-out "%{http_code}" \
      --max-time 5 http://127.0.0.1:3000/api/admin/settings
  '
)"
authenticated_status="$(
  docker exec "$grafana_container" /bin/sh -ec '
    curl --silent --show-error --output /dev/null --write-out "%{http_code}" \
      --max-time 5 --user "$GF_SECURITY_ADMIN_USER:$GF_SECURITY_ADMIN_PASSWORD" \
      http://127.0.0.1:3000/api/admin/settings
  '
)"

if [ "$anonymous_status" != "401" ]; then
  die "anonymous Grafana admin request returned HTTP $anonymous_status, expected 401"
fi
if [ "$authenticated_status" != "200" ]; then
  die "configured Grafana admin credentials returned HTTP $authenticated_status, expected 200"
fi

printf "Grafana admin password reset and authentication verified for container '%s'.\n" "$grafana_container"
