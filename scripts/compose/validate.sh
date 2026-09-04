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
    POSTGRES_PASSWORD=compose-validation \
      CLICKHOUSE_PASSWORD=compose-validation \
      MEILI_MASTER_KEY=compose-validation \
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

  # No service (except init-certs) should mount the whole ./certs tree or the
  # ./certs/issuers subtree, which would expose every issuer private key.
  if jq -e '[.services | to_entries[] | select(.key != "init-certs") | .value.volumes // [] | any(.target == "/certs" or ((.source // "") | test("(^|/)certs(/issuers)?/?$")))] | any' "$output_file" >/dev/null; then
    echo "$compose_file mounts whole ./certs tree (exposes all issuer private keys); mount only own private + pubs + trusted.json + needed TLS subtrees" >&2
    exit 1
  fi

  if [ "$compose_file" = "compose.prod.yaml" ] && ! jq -e '
    .services as $services
    | ($services.clickhouse.healthcheck.test[1] | contains("$${CLICKHOUSE_PASSWORD}"))
      and ($services["init-certs"].entrypoint[2] | contains("<password from_env=\"CLICKHOUSE_PASSWORD\"/>"))
      and ([
        "init-database-migration",
        "jobs-service",
        "workflow-worker",
        "joblogs-processor"
      ] | all(.[]; $services[.].environment.MEILISEARCH_MASTER_KEY == "compose-validation"))
  ' "$output_file" >/dev/null; then
    echo "$compose_file does not propagate ClickHouse or Meilisearch credentials consistently" >&2
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

validate_auth_bundle() {
  # Per-issuer JWT keys must be wired through build args, Compose init-certs, and K8s volumes.
  for svc in users-service workflows-service jobs-service notifications-service analytics-service scheduling-worker workflow-worker execution-worker runtime-agent joblogs-processor analytics-processor outbox-relay server; do
    if ! grep -q "PRIVATE_KEY_PATH=certs/issuers/$svc/auth.ed" "$root_dir/compose.dev.yaml"; then
      echo "compose.dev.yaml missing per-issuer key for $svc" >&2
      exit 1
    fi
    if ! grep -q "PUBLIC_KEY_PATH=certs/issuers/$svc/auth.ed.pub" "$root_dir/compose.dev.yaml"; then
      echo "compose.dev.yaml missing per-issuer public key for $svc" >&2
      exit 1
    fi
  done
  # database-migration reuses server key; ensure it is per-issuer as well
  if ! grep -q "PRIVATE_KEY_PATH=certs/issuers/server/auth.ed" "$root_dir/compose.dev.yaml"; then
    echo "compose.dev.yaml missing per-issuer key for database-migration (server)" >&2
    exit 1
  fi
  # init-certs must generate per-issuer keys and trusted bundle
  for f in compose.dev.yaml compose.prod.yaml; do
    if ! grep -q 'AUTH_ISSUERS=' "$root_dir/$f"; then
      echo "$f init-certs does not generate per-issuer keys (missing AUTH_ISSUERS)" >&2
      exit 1
    fi
    if ! grep -q 'issuers/trusted.json' "$root_dir/$f"; then
      echo "$f init-certs does not generate trusted.json bundle" >&2
      exit 1
    fi
    if grep -q 'cp -f /certs/issuers/server/auth.ed /certs/auth.ed' "$root_dir/$f"; then
      echo "$f still contains legacy auth alias (should be per-issuer only)" >&2
      exit 1
    fi
  done
  # init-certs cert-bootstrap must also be per-issuer
  if ! grep -q 'AUTH_ISSUERS=' "$root_dir/infra/k8s/overlays/local/cert-bootstrap.yaml"; then
    echo "infra/k8s/overlays/local/cert-bootstrap.yaml init-certs is not per-issuer" >&2
    exit 1
  fi
  if ! grep -q 'issuers/trusted.json' "$root_dir/infra/k8s/base/workloads.yaml"; then
    echo "infra/k8s/base/workloads.yaml does not mount trusted.json bundle" >&2
    exit 1
  fi
  for f in infra/k8s/overlays/local/cert-bootstrap.yaml scripts/k8s/setup.sh; do
    if grep -Fq '\"%s\": {\"iss\":' "$root_dir/$f"; then
      echo "$f escapes quotes inside a single-quoted printf and emits invalid JSON" >&2
      exit 1
    fi
  done
  local_kustomization="$root_dir/infra/k8s/overlays/local/kustomization.yaml"
  issuer_mounts=$(grep -c 'subPath: issuers/' "$local_kustomization")
  isolated_issuer_mounts=$(grep -B2 'subPath: issuers/' "$local_kustomization" | grep -c 'name: auth-certs')
  if [ "$issuer_mounts" -ne "$isolated_issuer_mounts" ] || ! grep -q 'claimName: auth-certs-pvc' "$root_dir/infra/k8s/overlays/local/cert-bootstrap.yaml"; then
    echo "local Kubernetes issuer material must use only the isolated auth-certs PVC" >&2
    exit 1
  fi
  # Makefile must inject per-issuer private key paths
  if ! grep -q 'certs/issuers/users-service/auth.ed' "$root_dir/Makefile"; then
    echo "Makefile does not inject per-issuer auth key paths" >&2
    exit 1
  fi
  # Auth package must expose kid-aware bundle
  if ! grep -q 'kidForKey' "$root_dir/internal/pkg/auth/bundle.go"; then
    echo "internal/pkg/auth/bundle.go missing kid handling" >&2
    exit 1
  fi
  if ! grep -q 'trusted.json' "$root_dir/internal/pkg/auth/bundle.go" && ! grep -q 'trusted.json' "$root_dir/internal/pkg/auth/auth.go"; then
    echo "internal/pkg/auth bundle verification missing trusted.json handling" >&2
    exit 1
  fi
}

validate_auth_bundle

validate_rotate_helper() {
	# Previous quoting regression (5f4da2b4): echo "..." inside run_in_certs "..."
	# truncated the outer sh -c argument and weakened 0440→0640. Guard it.
	if grep -q 'echo "backup of old private key failed"' "$root_dir/scripts/compose/rotate-auth-key.sh"; then
		echo "rotate-auth-key.sh helper contains echo \"...\" inside run_in_certs \"...\" — use single quotes" >&2
		exit 1
	fi
	if grep -q 'echo "public key install failed' "$root_dir/scripts/compose/rotate-auth-key.sh"; then
		echo "rotate-auth-key.sh helper contains echo \"...\" inside run_in_certs \"...\" — use single quotes" >&2
		exit 1
	fi
	if grep -q 'echo "no backup available' "$root_dir/scripts/compose/rotate-auth-key.sh"; then
		echo "rotate-auth-key.sh helper contains echo \"...\" inside run_in_certs \"...\" — use single quotes" >&2
		exit 1
	fi
	if grep -q 'chmod u+w [^;&|]*auth\.ed' "$root_dir/scripts/compose/rotate-auth-key.sh"; then
		echo "rotate-auth-key.sh should not chmod u+w live auth.ed files — directory write suffices" >&2
		exit 1
	fi
	# Bundle-derived old_kid is interpolated into the helper sh -c; it must be
	# allowlisted first or metacharacters break out of quoting (RCE as container root).
	if ! grep -q 'A-Za-z0-9_.:/-' "$root_dir/scripts/compose/rotate-auth-key.sh"; then
		echo "rotate-auth-key.sh must allowlist old_kid before helper interpolation" >&2
		exit 1
	fi
	if [ "$(grep -c 'force-recreate \$all_issuers' "$root_dir/scripts/compose/rotate-auth-key.sh")" -ne 2 ]; then
		echo "rotate-auth-key.sh must restart every auth service after pruning" >&2
		exit 1
	fi
	sh -n "$root_dir/scripts/compose/rotate-auth-key.sh" || {
		echo "rotate-auth-key.sh has shell syntax errors" >&2
		exit 1
	}
}

validate_rotate_helper

sh -n "$root_dir/scripts/compose/up.sh"

echo "Compose configurations, development secrets, and Docker proxy credential mounts are valid"
