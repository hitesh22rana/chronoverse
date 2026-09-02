#!/bin/sh
set -eu
unset CRYPTO_SECRET SERVER_CSRF_HMAC_SECRET

command -v docker >/dev/null 2>&1 || {
  echo "docker is required to validate Compose configuration" >&2
  exit 1
}
command -v jq >/dev/null 2>&1 || {
  echo "jq is required to validate Compose configuration" >&2
  exit 1
}

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

file_permissions() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}

render_compose() {
  compose_file=$1
  output_file=$2

  if [ "$compose_file" = "compose.prod.yaml" ]; then
    CRYPTO_SECRET=0123456789abcdef0123456789abcdef \
      SERVER_CSRF_HMAC_SECRET=abcdef0123456789abcdef0123456789 \
      GF_SECURITY_ADMIN_PASSWORD=compose-validation \
      docker compose -f "$root_dir/$compose_file" config --format json > "$output_file"
  else
    CRYPTO_SECRET=0123456789abcdef0123456789abcdef \
      SERVER_CSRF_HMAC_SECRET=abcdef0123456789abcdef0123456789 \
      docker compose -f "$root_dir/$compose_file" config --format json > "$output_file"
  fi
}

validate_compose() {
  compose_file=$1
  output_file="$tmp_dir/$compose_file.json"
  render_compose "$compose_file" "$output_file"

  nested_mounts=$(jq -r '
    [
      .services
      | to_entries[]
      | .key as $service
      | (.value.volumes // []) as $volumes
      | $volumes[] as $parent
      | select($parent.read_only == true)
      | $volumes[] as $child
      | select(
          $child.target != $parent.target
          and ($child.target | startswith($parent.target + "/"))
        )
      | "\($service): \($child.target) is nested below read-only \($parent.target)"
    ]
    | .[]
  ' "$output_file")
  if [ -n "$nested_mounts" ]; then
    echo "$compose_file contains mount targets that Docker cannot create:" >&2
    echo "$nested_mounts" >&2
    exit 1
  fi

  if ! jq -e '
    . as $root
    | ["runtime-agent", "workflow-worker", "execution-worker"]
    | all(
        .[];
        . as $role
        | $root.services[$role] as $service
        | ([
            $service.volumes[]?
            | select(
                .target == "/docker-proxy-certs"
                and .read_only == true
                and (.source | endswith("/" + $role))
              )
          ] | length == 1)
        and $service.environment.DOCKER_PROXY_TLS_CA_FILE == "/docker-proxy-certs/ca.crt"
        and $service.environment.DOCKER_PROXY_TLS_CERT_FILE == "/docker-proxy-certs/client.crt"
        and $service.environment.DOCKER_PROXY_TLS_KEY_FILE == "/docker-proxy-certs/client.key"
      )
  ' "$output_file" >/dev/null; then
    echo "$compose_file does not isolate every Docker proxy client role" >&2
    exit 1
  fi
}

validate_env_init() {
  env_file="$tmp_dir/.env"
  output_file="$tmp_dir/compose.dev.env.json"
  printf 'GRAFANA_HOST_PORT=3100\n' > "$env_file"
  original_checksum=$(cksum "$env_file")
  if CRYPTO_SECRET=0123456789abcdef0123456789abcdef \
    "$root_dir/scripts/compose/init-env.sh" "$env_file" >/dev/null 2>&1; then
    echo "dotenv initializer accepted an exported server secret" >&2
    exit 1
  fi
  [ "$original_checksum" = "$(cksum "$env_file")" ]

  mkdir "$tmp_dir/bin"
  printf '#!/bin/sh\nexit 1\n' > "$tmp_dir/bin/openssl"
  chmod +x "$tmp_dir/bin/openssl"
  chmod 644 "$env_file"
  if PATH="$tmp_dir/bin:$PATH" "$root_dir/scripts/compose/init-env.sh" "$env_file" >/dev/null 2>&1; then
    echo "dotenv initializer ignored an OpenSSL failure" >&2
    exit 1
  fi
  [ "$original_checksum" = "$(cksum "$env_file")" ]
  [ "$(file_permissions "$env_file")" = 600 ]

  printf 'CRYPTO_SECRET=${SEED}\nSERVER_CSRF_HMAC_SECRET=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789\n' > "$env_file"
  SEED=ambient-only "$root_dir/scripts/compose/init-env.sh" "$env_file" >/dev/null 2>&1
  grep -Eq '^CRYPTO_SECRET=[[:xdigit:]]{32}$' "$env_file"
  interpolation_checksum=$(cksum "$env_file")
  "$root_dir/scripts/compose/init-env.sh" "$env_file" >/dev/null 2>&1
  [ "$interpolation_checksum" = "$(cksum "$env_file")" ]

  printf 'GRAFANA_HOST_PORT=3100\nexport CRYPTO_SECRET=0123456789abcdef0123456789abcdef\nSERVER_CSRF_HMAC_SECRET="" # intentionally unset\n' > "$env_file"

  "$root_dir/scripts/compose/init-env.sh" "$env_file"
  first_checksum=$(cksum "$env_file")
  "$root_dir/scripts/compose/init-env.sh" "$env_file"

  [ "$first_checksum" = "$(cksum "$env_file")" ]
  grep -q '^export CRYPTO_SECRET=0123456789abcdef0123456789abcdef$' "$env_file"
  grep -q '^SERVER_CSRF_HMAC_SECRET="" # intentionally unset$' "$env_file"
  [ "$(grep -c '^GRAFANA_HOST_PORT=3100$' "$env_file")" -eq 1 ]
  [ "$(file_permissions "$env_file")" = 600 ]

  docker compose --env-file "$env_file" -f "$root_dir/compose.dev.yaml" config --format json > "$output_file"
  jq -e '
    .services.server.environment.CRYPTO_SECRET | length == 32
  ' "$output_file" >/dev/null
  jq -e '
    .services.server.environment.SERVER_CSRF_HMAC_SECRET | length == 64
  ' "$output_file" >/dev/null
}

validate_env_init
validate_compose compose.dev.yaml
validate_compose compose.prod.yaml
echo "Compose configurations, development secrets, and Docker proxy credential mounts are valid"
