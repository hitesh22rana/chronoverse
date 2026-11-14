#!/usr/bin/env bash

set -euo pipefail

grafana_container="${GRAFANA_CONTAINER:-lgtm}"
grafana_ready_timeout=60

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

wait_for_grafana() {
  local deadline=$((SECONDS + grafana_ready_timeout))

  while [ "$SECONDS" -lt "$deadline" ]; do
    if docker exec "$grafana_container" /bin/sh -ec '
      curl --silent --fail --output /dev/null --max-time 2 \
        http://127.0.0.1:3000/api/health
    ' >/dev/null 2>&1; then
      return
    fi

    if [ "$(docker container inspect --format '{{.State.Running}}' "$grafana_container" 2>/dev/null || true)" != "true" ]; then
      die "Grafana container '$grafana_container' stopped while waiting for readiness"
    fi
    sleep 1
  done

  die "Grafana in container '$grafana_container' did not become ready within ${grafana_ready_timeout}s"
}

if ! docker container inspect "$grafana_container" >/dev/null 2>&1; then
  die "Grafana container '$grafana_container' does not exist"
fi

if [ "$(docker container inspect --format '{{.State.Running}}' "$grafana_container")" != "true" ]; then
  die "Grafana container '$grafana_container' is not running"
fi

# Compose reports container startup before Grafana necessarily accepts HTTP
# requests. Wait before changing the database so a startup timeout cannot leave
# the password reset partially applied.
wait_for_grafana

docker exec -i "$grafana_container" /bin/sh -ec '
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
  if printf "%s" "$GF_SECURITY_ADMIN_USER" | od -An -tx1 | LC_ALL=C grep -q -E "(^| )(0[0-9a-f]|1[0-9a-f]|7f)( |$)"; then
    echo "error: GF_SECURITY_ADMIN_USER contains a control character and is invalid" >&2
    exit 1
  fi
  case "$GF_SECURITY_ADMIN_USER" in
    *:*)
      echo "error: GF_SECURITY_ADMIN_USER cannot contain a colon because Grafana Basic authentication uses it as a delimiter" >&2
      exit 1
      ;;
  esac
  printf "%s" "$GF_SECURITY_ADMIN_PASSWORD" | \
    /otel-lgtm/grafana/bin/grafana cli \
      --homepath /otel-lgtm/grafana \
      --configOverrides cfg:default.paths.data=/data/grafana/data \
      admin reset-admin-password --password-from-stdin
'

# Confirm Grafana is ready before verifying the configured credentials.
wait_for_grafana

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
