#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MODE=""
NAMESPACE="chronoverse"
CONTEXT=""
DRY_RUN=false
SKIP_APPLY=false
CREATE_KIND=false
STORAGE_CLASS=""
REALIP_CIDRS=""
INSECURE_DEFAULT_SECRET='a&1*~#^2^#!@#$%^&*()-_=+{}[]|<>?'

usage() {
  cat <<'EOF'
Usage: scripts/k8s/setup.sh [options]

Options:
  --mode local|production       Deployment strategy.
  --context <name>              kubectl context to use.
  --dry-run                     Validate and print dry-run apply output.
  --skip-apply                  Bootstrap prerequisites but do not apply manifests.
  --storage-class <name>        Production StorageClass override.
  --realip-cidrs <list>         Production client-IP trust ranges for nginx
                                rate limiting (comma/space separated). Overrides
                                pod-CIDR auto-detection.
  --create-kind                 Create the local kind cluster before applying local.
  -h, --help                    Show this help.
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

info() {
  echo "==> $*"
}

warn() {
  echo "warning: $*" >&2
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

is_tty() {
  [ -t 0 ]
}

kubectl_cmd() {
  if [ -n "$CONTEXT" ]; then
    kubectl --context "$CONTEXT" "$@"
  else
    kubectl "$@"
  fi
}

confirm() {
  local prompt="$1"
  local answer=""
  read -r -p "$prompt [y/N] " answer
  case "$answer" in
    y|Y|yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --mode)
      MODE="${2:-}"
      shift 2
      ;;
    --context)
      CONTEXT="${2:-}"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    --skip-apply)
      SKIP_APPLY=true
      shift
      ;;
    --storage-class)
      STORAGE_CLASS="${2:-}"
      shift 2
      ;;
    --realip-cidrs)
      REALIP_CIDRS="${2:-}"
      shift 2
      ;;
    --create-kind)
      CREATE_KIND=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

select_mode() {
  cat <<'EOF'
Choose a Kubernetes deployment strategy:

1) local
   Single-node, self-contained Kubernetes validation. Best for kind/minikube and
   development checks. Uses in-cluster infra, hostPath storage, one replica per
   app, and generated local bootstrap material.

2) production
   Self-hosted Chronoverse on your Kubernetes infrastructure. Uses in-cluster
   PostgreSQL, Redis, Kafka, ClickHouse, Meilisearch, runtime-agent, workers,
   HPAs, Kubernetes Secrets, and production storage.
EOF
  local choice=""
  read -r -p "Select mode [local/production]: " choice
  case "$choice" in
    local|1) MODE="local" ;;
    production|2) MODE="production" ;;
    *) die "mode must be local or production" ;;
  esac
}

[ -n "$MODE" ] || select_mode
[ "$MODE" = "local" ] || [ "$MODE" = "production" ] || die "--mode must be local or production"

need_cmd kubectl
need_cmd openssl
if [ "$MODE" = "production" ]; then
  need_cmd keytool
fi

KUSTOMIZE_DIR="$ROOT_DIR/infra/k8s/overlays/$MODE"
[ -d "$KUSTOMIZE_DIR" ] || die "missing overlay: $KUSTOMIZE_DIR"

CURRENT_CONTEXT="$(kubectl_cmd config current-context 2>/dev/null || true)"

if [ "$MODE" = "local" ] && [ "$DRY_RUN" = false ] && [ "$CREATE_KIND" = false ] && [ -z "$CONTEXT" ]; then
  if [ "$CURRENT_CONTEXT" = "docker-desktop" ]; then
    cat <<'EOF'
The current kubectl context is docker-desktop.

Docker Desktop's built-in Kubernetes cluster does not expose Docker Engine at
/var/run/docker.sock inside the node, so Chronoverse container workflows cannot
start the docker-proxy DaemonSet there.
EOF
    if is_tty && confirm "Create or reuse the repo's local kind cluster instead?"; then
      CREATE_KIND=true
    else
      die "use --create-kind or --context <compatible-context> for local Kubernetes setup"
    fi
  elif command -v kind >/dev/null 2>&1 && kind get clusters 2>/dev/null | grep -qx chronoverse; then
    if is_tty && confirm "Use existing kind-chronoverse context for local setup?"; then
      CONTEXT="kind-chronoverse"
      CURRENT_CONTEXT="$CONTEXT"
    fi
  fi
fi

if [ "$CREATE_KIND" = true ]; then
  need_cmd kind
  [ "$MODE" = "local" ] || die "--create-kind is only supported with --mode local"
  info "Preparing local kind cluster"
  if ! kind get clusters 2>/dev/null | grep -qx chronoverse; then
    kind create cluster --name chronoverse --config "$ROOT_DIR/infra/k8s/overlays/local/kind-cluster.yaml"
  fi
  CONTEXT="${CONTEXT:-kind-chronoverse}"
  CURRENT_CONTEXT="$CONTEXT"
fi

info "Using kubectl context: ${CURRENT_CONTEXT:-default}"

if [ "$DRY_RUN" = false ]; then
  info "Ensuring namespace $NAMESPACE exists"
  kubectl_cmd create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl_cmd apply -f -
fi

random_hex() {
  openssl rand -hex "${1:-24}"
}

secret_keys() {
  case "$1" in
    postgres-secret) echo "POSTGRES_USER POSTGRES_PASSWORD POSTGRES_DB" ;;
    clickhouse-secret) echo "CLICKHOUSE_PASSWORD" ;;
    meilisearch-secret) echo "MEILISEARCH_MASTER_KEY MEILI_MASTER_KEY" ;;
    kafka-tls-secret) echo "KAFKA_SSL_KEYSTORE_PASSWORD KAFKA_SSL_TRUSTSTORE_PASSWORD KAFKA_SSL_KEY_PASSWORD" ;;
    chronoverse-server-security) echo "CRYPTO_SECRET SERVER_CSRF_HMAC_SECRET" ;;
    chronoverse-auth) echo "auth.ed auth.ed.pub" ;;
    chronoverse-ca) echo "ca.crt" ;;
    chronoverse-ingress-tls) echo "tls.crt tls.key" ;;
    chronoverse-client-tls) echo "tls.crt tls.key" ;;
    chronoverse-service-tls) echo "users-service.crt users-service.key workflows-service.crt workflows-service.key jobs-service.crt jobs-service.key notifications-service.crt notifications-service.key analytics-service.crt analytics-service.key" ;;
    chronoverse-infra-tls) echo "postgres.crt postgres.key redis.crt redis.key clickhouse.crt clickhouse.key kafka.crt kafka.key meilisearch.crt meilisearch.key" ;;
    chronoverse-kafka-tls) echo "kafka.keystore.jks kafka.truststore.jks keystore_creds.txt truststore_creds.txt key_creds.txt" ;;
    *) die "unknown secret $1" ;;
  esac
}

secret_exists() {
  kubectl_cmd -n "$NAMESPACE" get secret "$1" >/dev/null 2>&1
}

validate_secret_complete() {
  local name="$1"
  local missing=()
  local key
  for key in $(secret_keys "$name"); do
    if ! kubectl_cmd -n "$NAMESPACE" get secret "$name" -o "go-template={{ index .data \"$key\" }}" 2>/dev/null | grep -q .; then
      missing+=("$key")
    fi
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    die "secret $name exists but is missing required key(s): ${missing[*]}"
  fi
}

secret_decoded_length() {
  local name="$1"
  local key="$2"
  local encoded
  encoded="$(kubectl_cmd -n "$NAMESPACE" get secret "$name" -o "go-template={{ index .data \"$key\" }}" 2>/dev/null || true)"
  if [ -z "$encoded" ]; then
    echo 0
    return
  fi
  printf '%s' "$encoded" | openssl base64 -d -A | wc -c | tr -d ' '
}

secret_decoded_value() {
  local name="$1"
  local key="$2"
  local encoded
  encoded="$(kubectl_cmd -n "$NAMESPACE" get secret "$name" -o "go-template={{ index .data \"$key\" }}" 2>/dev/null || true)"
  [ -n "$encoded" ] || return
  printf '%s' "$encoded" | openssl base64 -d -A
}

apply_secret_yaml() {
  local yaml="$1"
  if [ "$DRY_RUN" = true ]; then
    printf '%s\n' "$yaml" | kubectl_cmd apply --dry-run=client -f - >/dev/null
  else
    printf '%s\n' "$yaml" | kubectl_cmd apply -f -
  fi
}

create_literal_secret() {
  local name="$1"
  shift
  if secret_exists "$name"; then
    validate_secret_complete "$name"
    info "Keeping existing complete secret $name"
    return
  fi
  info "Creating missing secret $name"
  local yaml
  yaml="$(kubectl_cmd -n "$NAMESPACE" create secret generic "$name" "$@" --dry-run=client -o yaml)"
  apply_secret_yaml "$yaml"
}

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
  if [ -n "${PATCH_DIR:-}" ]; then
    rm -rf "$PATCH_DIR"
  fi
}
trap cleanup EXIT

create_data_secrets() {
  local postgres_password clickhouse_password meili_key kafka_keystore kafka_truststore crypto_secret csrf_secret
  if [ "$MODE" = "local" ]; then
    postgres_password="chronoverse-local-postgres-password"
    clickhouse_password="chronoverse-local-clickhouse-password"
    meili_key="chronoverse-local-meilisearch-master-key"
    kafka_keystore="chronoverse-local-kafka-keystore-password"
    kafka_truststore="chronoverse-local-kafka-truststore-password"
    crypto_secret="chronoverse-local-crypto-key-001"
    csrf_secret="chronoverse-local-csrf-hmac-0001"
  else
    postgres_password="$(random_hex 24)"
    clickhouse_password="$(random_hex 24)"
    meili_key="$(random_hex 32)"
    kafka_keystore="$(random_hex 18)"
    kafka_truststore="$(random_hex 18)"
    crypto_secret="$(random_hex 16)"
    csrf_secret="$(random_hex 32)"
  fi

  create_literal_secret postgres-secret \
    --from-literal=POSTGRES_USER=postgres \
    --from-literal=POSTGRES_PASSWORD="$postgres_password" \
    --from-literal=POSTGRES_DB=chronoverse

  create_literal_secret clickhouse-secret \
    --from-literal=CLICKHOUSE_PASSWORD="$clickhouse_password"

  create_literal_secret meilisearch-secret \
    --from-literal=MEILISEARCH_MASTER_KEY="$meili_key" \
    --from-literal=MEILI_MASTER_KEY="$meili_key"

  create_literal_secret kafka-tls-secret \
    --from-literal=KAFKA_SSL_KEYSTORE_PASSWORD="$kafka_keystore" \
    --from-literal=KAFKA_SSL_TRUSTSTORE_PASSWORD="$kafka_truststore" \
    --from-literal=KAFKA_SSL_KEY_PASSWORD="$kafka_keystore"

  create_server_security_secret "$crypto_secret" "$csrf_secret"
}

create_server_security_secret() {
  local crypto_secret="$1"
  local csrf_secret="$2"
  local secret_name="chronoverse-server-security"

  if secret_exists "$secret_name"; then
    validate_secret_complete "$secret_name"

    local crypto_len existing_crypto_secret existing_csrf_secret invalid_reason
    crypto_len="$(secret_decoded_length "$secret_name" CRYPTO_SECRET)"
    existing_crypto_secret="$(secret_decoded_value "$secret_name" CRYPTO_SECRET)"
    existing_csrf_secret="$(secret_decoded_value "$secret_name" SERVER_CSRF_HMAC_SECRET)"
    invalid_reason=""

    if [ "$existing_crypto_secret" = "$INSECURE_DEFAULT_SECRET" ] || [ "$existing_csrf_secret" = "$INSECURE_DEFAULT_SECRET" ]; then
      invalid_reason="it contains the known insecure development placeholder"
    elif [ "$existing_crypto_secret" = "$existing_csrf_secret" ]; then
      invalid_reason="CRYPTO_SECRET and SERVER_CSRF_HMAC_SECRET must be different"
    elif [ "$crypto_len" != "32" ]; then
      invalid_reason="CRYPTO_SECRET is $crypto_len bytes; expected 32"
    fi

    if [ -z "$invalid_reason" ]; then
      info "Keeping existing complete secret $secret_name"
      return
    fi

    if [ "$MODE" != "local" ]; then
      die "secret $secret_name is invalid: $invalid_reason"
    fi

    info "Replacing invalid local secret $secret_name because $invalid_reason"
    if [ "$DRY_RUN" = true ]; then
      local yaml
      yaml="$(kubectl_cmd -n "$NAMESPACE" create secret generic "$secret_name" \
        --from-literal=CRYPTO_SECRET="$crypto_secret" \
        --from-literal=SERVER_CSRF_HMAC_SECRET="$csrf_secret" \
        --dry-run=client -o yaml)"
      apply_secret_yaml "$yaml"
      return
    fi

    kubectl_cmd -n "$NAMESPACE" delete secret "$secret_name" --ignore-not-found >/dev/null
  fi

  create_literal_secret "$secret_name" \
    --from-literal=CRYPTO_SECRET="$crypto_secret" \
    --from-literal=SERVER_CSRF_HMAC_SECRET="$csrf_secret"
}

generate_ca() {
  openssl genrsa -out "$TMP_DIR/ca.key" 4096 >/dev/null 2>&1
  openssl req -x509 -new -nodes -key "$TMP_DIR/ca.key" -sha256 -days 3650 -out "$TMP_DIR/ca.crt" -subj "/CN=Chronoverse Kubernetes CA" >/dev/null 2>&1
}

generate_cert() {
  local name="$1"
  local cn="$2"
  local alt_names="$3"
  openssl genrsa -out "$TMP_DIR/$name.key" 4096 >/dev/null 2>&1
  openssl req -new -key "$TMP_DIR/$name.key" -out "$TMP_DIR/$name.csr" -subj "/CN=$cn" >/dev/null 2>&1
  printf "subjectAltName=%s\n" "$alt_names" > "$TMP_DIR/$name-ext.cnf"
  openssl x509 -req -in "$TMP_DIR/$name.csr" -CA "$TMP_DIR/ca.crt" -CAkey "$TMP_DIR/ca.key" -CAcreateserial -out "$TMP_DIR/$name.crt" -days 3650 -extfile "$TMP_DIR/$name-ext.cnf" >/dev/null 2>&1
}

create_file_secret() {
  local name="$1"
  shift
  if secret_exists "$name"; then
    validate_secret_complete "$name"
    info "Keeping existing complete secret $name"
    return
  fi
  info "Creating missing secret $name"
  local yaml
  yaml="$(kubectl_cmd -n "$NAMESPACE" create secret generic "$name" "$@" --dry-run=client -o yaml)"
  apply_secret_yaml "$yaml"
}

create_tls_secret() {
  local name="$1"
  local cert="$2"
  local key="$3"
  if secret_exists "$name"; then
    validate_secret_complete "$name"
    info "Keeping existing complete secret $name"
    return
  fi
  info "Creating missing secret $name"
  local yaml
  yaml="$(kubectl_cmd -n "$NAMESPACE" create secret tls "$name" --cert="$cert" --key="$key" --dry-run=client -o yaml)"
  apply_secret_yaml "$yaml"
}

create_ingress_tls_secret() {
  if secret_exists chronoverse-ingress-tls; then
    validate_secret_complete chronoverse-ingress-tls
    info "Keeping existing complete secret chronoverse-ingress-tls"
    return
  fi

  info "Generating missing production ingress TLS material"
  openssl req -x509 -newkey rsa:4096 -nodes \
    -keyout "$TMP_DIR/ingress.key" \
    -out "$TMP_DIR/ingress.crt" \
    -sha256 -days 365 \
    -subj "/CN=chronoverse.example.com" \
    -addext "subjectAltName=DNS:chronoverse.example.com" >/dev/null 2>&1
  create_tls_secret chronoverse-ingress-tls "$TMP_DIR/ingress.crt" "$TMP_DIR/ingress.key"
}

create_auth_secret() {
  if secret_exists chronoverse-auth; then
    validate_secret_complete chronoverse-auth
    return
  fi

  info "Generating missing production auth material"
  openssl genpkey -algorithm ED25519 -outform pem -out "$TMP_DIR/auth.ed" >/dev/null 2>&1
  openssl pkey -in "$TMP_DIR/auth.ed" -pubout -out "$TMP_DIR/auth.ed.pub" >/dev/null 2>&1
  create_file_secret chronoverse-auth \
    --from-file=auth.ed="$TMP_DIR/auth.ed" \
    --from-file=auth.ed.pub="$TMP_DIR/auth.ed.pub"
}

create_production_tls_secrets() {
  local tls_required=(
    chronoverse-ca
    chronoverse-client-tls
    chronoverse-service-tls
    chronoverse-infra-tls
    chronoverse-kafka-tls
  )
  create_auth_secret
  create_ingress_tls_secret

  local existing_count=0
  local secret
  for secret in "${tls_required[@]}"; do
    if secret_exists "$secret"; then
      validate_secret_complete "$secret"
      existing_count=$((existing_count + 1))
    fi
  done
  if [ "$existing_count" -eq "${#tls_required[@]}" ]; then
    return
  fi
  if [ "$existing_count" -gt 0 ]; then
    die "production TLS secrets are an atomic trust chain; either provide all of ${tls_required[*]} or delete the partial set and rerun setup"
  fi

  info "Generating missing production TLS material"
  generate_ca
  local svc
  for svc in users-service workflows-service jobs-service notifications-service analytics-service postgres redis clickhouse kafka meilisearch; do
    generate_cert "$svc" "$svc" "DNS:$svc,DNS:$svc.$NAMESPACE,DNS:$svc.$NAMESPACE.svc,DNS:$svc.$NAMESPACE.svc.cluster.local,IP:127.0.0.1"
  done
  generate_cert client chronoverse-client "DNS:client"

  create_file_secret chronoverse-ca \
    --from-file=ca.crt="$TMP_DIR/ca.crt"
  create_file_secret chronoverse-client-tls \
    --from-file=tls.crt="$TMP_DIR/client.crt" \
    --from-file=tls.key="$TMP_DIR/client.key"
  create_file_secret chronoverse-service-tls \
    --from-file=users-service.crt="$TMP_DIR/users-service.crt" \
    --from-file=users-service.key="$TMP_DIR/users-service.key" \
    --from-file=workflows-service.crt="$TMP_DIR/workflows-service.crt" \
    --from-file=workflows-service.key="$TMP_DIR/workflows-service.key" \
    --from-file=jobs-service.crt="$TMP_DIR/jobs-service.crt" \
    --from-file=jobs-service.key="$TMP_DIR/jobs-service.key" \
    --from-file=notifications-service.crt="$TMP_DIR/notifications-service.crt" \
    --from-file=notifications-service.key="$TMP_DIR/notifications-service.key" \
    --from-file=analytics-service.crt="$TMP_DIR/analytics-service.crt" \
    --from-file=analytics-service.key="$TMP_DIR/analytics-service.key"
  create_file_secret chronoverse-infra-tls \
    --from-file=postgres.crt="$TMP_DIR/postgres.crt" \
    --from-file=postgres.key="$TMP_DIR/postgres.key" \
    --from-file=redis.crt="$TMP_DIR/redis.crt" \
    --from-file=redis.key="$TMP_DIR/redis.key" \
    --from-file=clickhouse.crt="$TMP_DIR/clickhouse.crt" \
    --from-file=clickhouse.key="$TMP_DIR/clickhouse.key" \
    --from-file=kafka.crt="$TMP_DIR/kafka.crt" \
    --from-file=kafka.key="$TMP_DIR/kafka.key" \
    --from-file=meilisearch.crt="$TMP_DIR/meilisearch.crt" \
    --from-file=meilisearch.key="$TMP_DIR/meilisearch.key"

  local keystore_password truststore_password
  keystore_password="$(random_hex 18)"
  truststore_password="$(random_hex 18)"
  openssl pkcs12 -export -in "$TMP_DIR/kafka.crt" -inkey "$TMP_DIR/kafka.key" -certfile "$TMP_DIR/ca.crt" -out "$TMP_DIR/kafka.p12" -name kafka -password "pass:$keystore_password" >/dev/null 2>&1
  keytool -importkeystore -deststorepass "$keystore_password" -destkeypass "$keystore_password" -destkeystore "$TMP_DIR/kafka.keystore.jks" -srckeystore "$TMP_DIR/kafka.p12" -srcstoretype PKCS12 -srcstorepass "$keystore_password" -alias kafka >/dev/null 2>&1
  keytool -import -trustcacerts -alias CARoot -file "$TMP_DIR/ca.crt" -keystore "$TMP_DIR/kafka.truststore.jks" -storepass "$truststore_password" -noprompt >/dev/null 2>&1
  printf "%s\n" "$keystore_password" > "$TMP_DIR/keystore_creds.txt"
  printf "%s\n" "$truststore_password" > "$TMP_DIR/truststore_creds.txt"
  printf "%s\n" "$keystore_password" > "$TMP_DIR/key_creds.txt"
  create_file_secret chronoverse-kafka-tls \
    --from-file=kafka.keystore.jks="$TMP_DIR/kafka.keystore.jks" \
    --from-file=kafka.truststore.jks="$TMP_DIR/kafka.truststore.jks" \
    --from-file=keystore_creds.txt="$TMP_DIR/keystore_creds.txt" \
    --from-file=truststore_creds.txt="$TMP_DIR/truststore_creds.txt" \
    --from-file=key_creds.txt="$TMP_DIR/key_creds.txt"

}

check_storage() {
  if [ "$MODE" != "production" ]; then
    return
  fi
  if [ -n "$STORAGE_CLASS" ]; then
    info "Using requested production StorageClass: $STORAGE_CLASS"
    return
  fi
  if kubectl_cmd get storageclass -o jsonpath='{range .items[?(@.metadata.annotations.storageclass\.kubernetes\.io/is-default-class=="true")]}{.metadata.name}{"\n"}{end}' 2>/dev/null | grep -q .; then
    info "Using cluster default StorageClass"
    return
  fi
  if [ "$DRY_RUN" = true ]; then
    info "No default StorageClass found during dry-run; production apply will require --storage-class"
    return
  fi
  if confirm "No default StorageClass was found. Continue only if your PVCs will bind through another provisioner?"; then
    return
  fi
  die "production requires a default StorageClass, --storage-class, or explicit storage provisioning"
}

delete_bootstrap_jobs() {
  if [ "$DRY_RUN" = true ] || [ "$SKIP_APPLY" = true ]; then
    return
  fi

  local jobs=(database-migration init-kafka-topics)
  if [ "$MODE" = "local" ]; then
    jobs+=(init-certs init-service-certs)
  fi

  info "Recreating bootstrap jobs for $MODE apply"
  kubectl_cmd -n "$NAMESPACE" delete job "${jobs[@]}" --ignore-not-found >/dev/null
}

create_data_secrets
if [ "$MODE" = "production" ]; then
  create_production_tls_secrets
fi
check_storage

if [ "$SKIP_APPLY" = true ]; then
  info "Skipping manifest apply"
  exit 0
fi

# Detect the cluster's node pod CIDRs so the nginx ingress can recover real
# client addresses from X-Forwarded-For (see nginx-realip-config). Production
# only: the local overlay's kind default is already correct.
REALIP_DETECTED=""
if [ "$MODE" = "production" ]; then
  if [ -n "$REALIP_CIDRS" ]; then
    # Operator override via --realip-cidrs: normalize commas to spaces.
    REALIP_DETECTED="$(echo "$REALIP_CIDRS" | tr ',' ' ' | tr -s ' ' | sed 's/^ //; s/ $//')"
    info "Using operator-provided client-IP trust ranges: $REALIP_DETECTED"
  else
    # Trust exactly where the ingress-nginx controllers connect from:
    # hostNetwork controllers connect from node addresses, pod-network
    # controllers from their pod addresses. The Go template emits an
    # explicit boolean because PodSpec.hostNetwork is omitempty — pods
    # omit it entirely, which would shift jsonpath columns.
    append_ip_prefix() {
      case "$1" in
        *:*) echo "$1/128" ;;
        *)   echo "$1/32" ;;
      esac
    }

    # Pipe-delimited with "-" placeholders: podIP and hostIP are optional
    # status fields (a Pending pod may not have them yet), and whitespace
    # separation would shift columns when one is absent.
    if ! CONTROLLER_ROWS="$(kubectl_cmd get pods -A -l app.kubernetes.io/component=controller,app.kubernetes.io/name=ingress-nginx -o go-template='{{range .items}}{{if .spec.hostNetwork}}true{{else}}false{{end}}|{{if .status.podIP}}{{.status.podIP}}{{else}}-{{end}}|{{if .status.hostIP}}{{.status.hostIP}}{{else}}-{{end}}{{"\n"}}{{end}}' 2>/dev/null)"; then
      warn "could not query ingress-nginx controller pods"
      CONTROLLER_ROWS=""
    fi

    HOSTNET_IPS=""
    PODNET_IPS=""
    while IFS='|' read -r hn pod_ip host_ip; do
      [ -n "${hn:-}" ] || continue
      case "$hn" in
        true)  [ -n "$host_ip" ] && [ "$host_ip" != "-" ] && HOSTNET_IPS="$HOSTNET_IPS $host_ip" ;;
        false) [ -n "$pod_ip" ] && [ "$pod_ip" != "-" ] && PODNET_IPS="$PODNET_IPS $pod_ip" ;;
      esac
    done <<EOF
$CONTROLLER_ROWS
EOF
    HOSTNET_IPS="$(echo $HOSTNET_IPS | tr ' ' '\n' | sed '/^$/d' | sort -u | tr '\n' ' ' | sed 's/ $//')"
    PODNET_IPS="$(echo $PODNET_IPS | tr ' ' '\n' | sed '/^$/d' | sort -u | tr '\n' ' ' | sed 's/ $//')"

    NODE_POD_CIDRS=""
    if ! NODE_POD_CIDRS="$(kubectl_cmd get nodes -o jsonpath='{.items[*].spec.podCIDRs[*]}' 2>/dev/null | tr ' ' '\n' | sed '/^$/d' | sort -u | tr '\n' ' ' | sed 's/ $//')"; then
      warn "could not query node pod CIDRs"
      NODE_POD_CIDRS=""
    fi
    if [ -z "$NODE_POD_CIDRS" ]; then
      if ! NODE_POD_CIDRS="$(kubectl_cmd get nodes -o jsonpath='{.items[*].spec.podCIDR}' 2>/dev/null | tr ' ' '\n' | sed '/^$/d' | sort -u | tr '\n' ' ' | sed 's/ $//')"; then
        warn "could not query node pod CIDRs (legacy field)"
        NODE_POD_CIDRS=""
      fi
    fi

    # Assemble independently: hostNetwork controllers contribute their node
    # address; pod-network controllers are covered by node pod CIDRs when
    # the cluster allocates them, otherwise by their current pod addresses.
    TRUST_IPS=""
    if [ -n "$HOSTNET_IPS" ]; then
      # hostNetwork controllers connect from node addresses. Trust every
      # node InternalIP so reschedules or scale-out to existing nodes stay
      # trusted; nodes added after this setup run still require a re-run
      # or an explicit --realip-cidrs with the node range.
      if ! NODE_IPS="$(kubectl_cmd get nodes -o jsonpath='{.items[*].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null | tr ' ' '\n' | sed '/^$/d' | sort -u | tr '\n' ' ' | sed 's/ $//')"; then
        warn "could not query node addresses"
        NODE_IPS=""
      fi
      if [ -z "$NODE_IPS" ]; then
        die "cannot determine host-network node address range — pass --realip-cidrs <list> with the ingress-nginx node range (e.g. the cluster's node InternalIPs) so per-client rate limits survive controller rescheduling (node listing failed or returned empty while hostNetwork controllers were detected)"
      fi
      TRUST_IPS="$(for ip in $NODE_IPS; do append_ip_prefix "$ip"; done | tr '\n' ' ' | sed 's/ $//')"
      warn "hostNetwork trust ranges cover the cluster's current nodes — nodes added later need a setup re-run, or set --realip-cidrs with the node range for stability"
    fi
    if [ -n "$PODNET_IPS" ]; then
      if [ -n "$NODE_POD_CIDRS" ]; then
        TRUST_IPS="$TRUST_IPS $NODE_POD_CIDRS"
      else
        TRUST_IPS="$TRUST_IPS $(for ip in $PODNET_IPS; do append_ip_prefix "$ip"; done | tr '\n' ' ' | sed 's/ $//')"
        warn "trusted ranges derived from current ingress-nginx pod IPs; these are ephemeral — prefer --realip-cidrs with a stable range for production"
      fi
    fi
    if [ -z "$TRUST_IPS" ]; then
      # Controller mode could not be determined (query failed, or no
      # controller pods matched the labels). The production overlay ships
      # a placeholder trust list, so applying anything here would either
      # break hostNetwork deployments or stomp a previously-correct
      # configuration. Refuse to guess: the operator must state the range.
      die "cannot determine the ingress-nginx client-IP source range — pass --realip-cidrs <list> (the addresses your ingress-nginx controllers connect from) so per-client rate limits match this deployment"
    fi

    if [ -n "$TRUST_IPS" ]; then
      REALIP_DETECTED="$(echo $TRUST_IPS | tr ' ' '\n' | sed '/^$/d' | sort -u | tr '\n' ' ' | sed 's/ $//')"
      if [ -n "$HOSTNET_IPS" ]; then
        info "Client-IP trust ranges include hostNetwork controller node addresses: $REALIP_DETECTED"
      else
        info "Detected client-IP trust ranges for nginx: $REALIP_DETECTED"
      fi
    fi
  fi
  REALIP_CIDRS="$REALIP_DETECTED"
  if [ -z "$REALIP_CIDRS" ]; then
    warn "could not detect client-IP trust ranges; keeping the placeholder range in infra/k8s/overlays/production/nginx-realip.yaml — set it for your cluster (or pass --realip-cidrs), otherwise every client shares one rate-limit bucket"
  fi
fi

USE_PATCH_DIR=false
if [ -n "$STORAGE_CLASS" ] || [ -n "$REALIP_CIDRS" ]; then
  USE_PATCH_DIR=true
  PATCH_DIR="$(mktemp -d "$ROOT_DIR/.k8s-setup.XXXXXX")"
  mkdir -p "$PATCH_DIR"
  {
    cat <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
- ../infra/k8s/overlays/$MODE
patches:
EOF
    if [ -n "$STORAGE_CLASS" ]; then
      cat <<EOF
- target:
    kind: PersistentVolumeClaim
  patch: |-
    - op: add
      path: /spec/storageClassName
      value: $STORAGE_CLASS
EOF
    fi
    if [ -n "$REALIP_CIDRS" ]; then
      echo "- path: nginx-realip-patch.yaml"
    fi
  } > "$PATCH_DIR/kustomization.yaml"
  if [ -n "$REALIP_CIDRS" ]; then
    {
      echo "apiVersion: v1"
      echo "kind: ConfigMap"
      echo "metadata:"
      echo "  name: nginx-realip-config"
      echo "data:"
      echo "  realip.conf: |"
      echo "    # Auto-generated by scripts/k8s/setup.sh from the cluster's node pod CIDRs."
      for cidr in $REALIP_CIDRS; do
        echo "    set_real_ip_from $cidr;"
      done
      echo "    real_ip_header X-Forwarded-For;"
      echo "    real_ip_recursive on;"
    } > "$PATCH_DIR/nginx-realip-patch.yaml"
  fi
  KUSTOMIZE_DIR="$PATCH_DIR"
fi

if [ "$SKIP_APPLY" = true ]; then
  info "Skipping manifest apply"
  exit 0
fi

delete_bootstrap_jobs

if [ "$DRY_RUN" = true ]; then
  info "Running dry-run apply for $MODE"
  if [ "$USE_PATCH_DIR" = true ]; then
    kubectl_cmd kustomize --load-restrictor=LoadRestrictionsNone "$KUSTOMIZE_DIR" | kubectl_cmd apply --dry-run=client --validate=false -f -
  else
    kubectl_cmd apply --dry-run=client --validate=false -k "$KUSTOMIZE_DIR"
  fi
else
  info "Applying $MODE overlay"
  if [ "$USE_PATCH_DIR" = true ]; then
    kubectl_cmd kustomize --load-restrictor=LoadRestrictionsNone "$KUSTOMIZE_DIR" | kubectl_cmd apply -f -
  else
    kubectl_cmd apply -k "$KUSTOMIZE_DIR"
  fi

  # ConfigMap volume updates reach the mounted file only via kubelet sync,
  # and nginx never re-reads configuration on its own. When the realip trust
  # list changed, roll the nginx Deployment so the new ranges take effect
  # immediately instead of trusting stale addresses indefinitely.
  if [ -n "$REALIP_CIDRS" ]; then
    info "Restarting nginx to apply the client-IP trust ranges"
    kubectl_cmd -n "$NAMESPACE" rollout restart deployment/nginx
    kubectl_cmd -n "$NAMESPACE" rollout status deployment/nginx --timeout=120s ||
      warn "nginx rollout did not complete in time; check pod logs"
  fi
  KUBECTL_CONTEXT_PREFIX=""
  if [ -n "$CONTEXT" ]; then
    KUBECTL_CONTEXT_PREFIX="--context $CONTEXT "
  fi
  cat <<EOF

Chronoverse Kubernetes resources were applied.

Watch pod rollout:
  kubectl ${KUBECTL_CONTEXT_PREFIX}-n $NAMESPACE get pods -w

Check jobs and daemonsets:
  kubectl ${KUBECTL_CONTEXT_PREFIX}-n $NAMESPACE get jobs,ds

EOF
  if [ "$MODE" = "local" ]; then
    cat <<EOF
Open the dashboard/API locally:
  kubectl ${KUBECTL_CONTEXT_PREFIX}-n $NAMESPACE port-forward svc/nginx 8080:80
  http://localhost:8080

Open LGTM locally:
  kubectl ${KUBECTL_CONTEXT_PREFIX}-n $NAMESPACE port-forward svc/lgtm 3000:3000
  http://localhost:3000

EOF
  else
    cat <<EOF
Production access:
  Chronoverse is exposed through the Kubernetes Ingress named chronoverse.
  The default host is chronoverse.example.com; replace it with your domain and
  point DNS to your ingress controller external address. The generated fallback
  ingress TLS certificate is self-signed; use a trusted certificate before
  exposing production traffic.

Authentication cookies are issued for SERVER_HOST_URL. Do not use
http://localhost port-forwarding as the normal production access URL unless you
also override SERVER_HOST_URL, SERVER_FRONTEND_URL, and SERVER_ALLOWED_ORIGINS
for a local-only smoke test.

Inspect the production ingress:
  kubectl ${KUBECTL_CONTEXT_PREFIX}-n $NAMESPACE get ingress chronoverse

Check the ingress controller address:
  kubectl ${KUBECTL_CONTEXT_PREFIX}get ingressclass
  kubectl ${KUBECTL_CONTEXT_PREFIX}get svc -A | grep -E 'ingress|nginx'

EOF
  fi
  cat <<EOF
Check registered runtimes:
  kubectl ${KUBECTL_CONTEXT_PREFIX}-n $NAMESPACE exec postgres-0 -- sh -c 'PGPASSWORD="\$POSTGRES_PASSWORD" psql -U "\$POSTGRES_USER" -d "\$POSTGRES_DB" -c "select id,node_name,docker_endpoint,status from runtime_nodes;"'
EOF
fi
