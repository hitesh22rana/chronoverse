#!/usr/bin/env bash

set -euo pipefail

grafana_container="${GRAFANA_CONTAINER:-lgtm}"
grafana_current_admin_user="${GRAFANA_CURRENT_ADMIN_USER:-admin}"

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
  printf "%s" "$GF_SECURITY_ADMIN_PASSWORD" | \
    /otel-lgtm/grafana/bin/grafana cli \
      --homepath /otel-lgtm/grafana \
      --configOverrides cfg:default.paths.data=/data/grafana/data \
      admin reset-admin-password --password-from-stdin
'

docker exec \
  --env "GRAFANA_CURRENT_ADMIN_USER=$grafana_current_admin_user" \
  "$grafana_container" /bin/sh -ec '
  auth_status() {
    curl --silent --show-error --output /dev/null --write-out "%{http_code}" \
      --max-time 5 --user "$1:$GF_SECURITY_ADMIN_PASSWORD" \
      http://127.0.0.1:3000/api/admin/settings
  }

  configured_status="$(auth_status "$GF_SECURITY_ADMIN_USER")"
  if [ "$configured_status" = "200" ]; then
    exit 0
  fi

  current_status="$(auth_status "$GRAFANA_CURRENT_ADMIN_USER")"
  if [ "$current_status" != "200" ]; then
    echo "error: neither GF_SECURITY_ADMIN_USER nor GRAFANA_CURRENT_ADMIN_USER authenticates after the password reset; set GRAFANA_CURRENT_ADMIN_USER to the login stored in the existing Grafana database" >&2
    exit 1
  fi

  case "$GF_SECURITY_ADMIN_USER" in
    *[![:print:]]*)
      echo "error: GF_SECURITY_ADMIN_USER contains a control character and cannot be migrated safely" >&2
      exit 1
      ;;
    *:*)
      echo "error: GF_SECURITY_ADMIN_USER cannot contain a colon because Grafana Basic authentication uses it as a delimiter" >&2
      exit 1
      ;;
  esac

  escaped_admin_user="$(
    printf "%s" "$GF_SECURITY_ADMIN_USER" |
      sed -e "s/\\\\/\\\\\\\\/g" -e "s/\"/\\\\\"/g"
  )"
  update_payload="$(printf "{\"login\":\"%s\"}" "$escaped_admin_user")"
  update_status="$(
    curl --silent --show-error --output /dev/null --write-out "%{http_code}" \
      --max-time 5 --user "$GRAFANA_CURRENT_ADMIN_USER:$GF_SECURITY_ADMIN_PASSWORD" \
      --request PUT --header "Content-Type: application/json" \
      --data-binary "$update_payload" \
      http://127.0.0.1:3000/api/users/1
  )"
  if [ "$update_status" != "200" ]; then
    echo "error: Grafana admin login migration returned HTTP $update_status, expected 200" >&2
    exit 1
  fi

  printf "Grafana admin login migrated from %s to %s.\n" \
    "$GRAFANA_CURRENT_ADMIN_USER" "$GF_SECURITY_ADMIN_USER"
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
