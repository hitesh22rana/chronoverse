#!/bin/sh
set -eu

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

render_compose() {
  compose_file=$1
  output_file=$2

  if [ "$compose_file" = "compose.prod.yaml" ]; then
    CRYPTO_SECRET=0123456789abcdef0123456789abcdef \
      SERVER_CSRF_HMAC_SECRET=abcdef0123456789abcdef0123456789 \
      GF_SECURITY_ADMIN_PASSWORD=compose-validation \
      docker compose -f "$root_dir/$compose_file" config --format json > "$output_file"
  else
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

validate_compose compose.dev.yaml
validate_compose compose.prod.yaml
echo "Compose configurations and Docker proxy credential mounts are valid"
