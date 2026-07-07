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
HOSTPATH_FALLBACK=false

usage() {
  cat <<'EOF'
Usage: scripts/k8s/setup.sh [options]

Options:
  --mode local|production       Deployment strategy.
  --namespace <name>            Kubernetes namespace. Default: chronoverse.
  --context <name>              kubectl context to use.
  --dry-run                     Validate and print dry-run apply output.
  --skip-apply                  Bootstrap prerequisites but do not apply manifests.
  --storage-class <name>        Production StorageClass override.
  --hostpath-fallback           Allow production hostPath fallback warning path.
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
    --namespace)
      NAMESPACE="${2:-}"
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
    --hostpath-fallback)
      HOSTPATH_FALLBACK=true
      shift
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
[ -n "$NAMESPACE" ] || die "--namespace cannot be empty"

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
    chronoverse-auth) echo "auth.ed auth.ed.pub" ;;
    chronoverse-ca) echo "ca.crt" ;;
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
    if ! kubectl_cmd -n "$NAMESPACE" get secret "$name" -o "jsonpath={.data['$key']}" 2>/dev/null | grep -q .; then
      missing+=("$key")
    fi
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    die "secret $name exists but is missing required key(s): ${missing[*]}"
  fi
  info "Keeping existing complete secret $name"
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
  create_literal_secret postgres-secret \
    --from-literal=POSTGRES_USER=postgres \
    --from-literal=POSTGRES_PASSWORD="$(random_hex 24)" \
    --from-literal=POSTGRES_DB=chronoverse

  create_literal_secret clickhouse-secret \
    --from-literal=CLICKHOUSE_PASSWORD="$(random_hex 24)"

  local meili_key
  meili_key="$(random_hex 32)"
  create_literal_secret meilisearch-secret \
    --from-literal=MEILISEARCH_MASTER_KEY="$meili_key" \
    --from-literal=MEILI_MASTER_KEY="$meili_key"

  local kafka_keystore kafka_truststore
  kafka_keystore="$(random_hex 18)"
  kafka_truststore="$(random_hex 18)"
  create_literal_secret kafka-tls-secret \
    --from-literal=KAFKA_SSL_KEYSTORE_PASSWORD="$kafka_keystore" \
    --from-literal=KAFKA_SSL_TRUSTSTORE_PASSWORD="$kafka_truststore" \
    --from-literal=KAFKA_SSL_KEY_PASSWORD="$kafka_keystore"
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
    return
  fi
  info "Creating missing secret $name"
  local yaml
  yaml="$(kubectl_cmd -n "$NAMESPACE" create secret generic "$name" "$@" --dry-run=client -o yaml)"
  apply_secret_yaml "$yaml"
}

create_production_tls_secrets() {
  local required=(
    chronoverse-auth
    chronoverse-ca
    chronoverse-client-tls
    chronoverse-service-tls
    chronoverse-infra-tls
    chronoverse-kafka-tls
  )
  local existing_complete=true
  local secret
  for secret in "${required[@]}"; do
    if secret_exists "$secret"; then
      validate_secret_complete "$secret"
    else
      existing_complete=false
    fi
  done
  if [ "$existing_complete" = true ]; then
    return
  fi

  info "Generating missing production TLS/auth material"
  generate_ca
  openssl genpkey -algorithm ED25519 -outform pem -out "$TMP_DIR/auth.ed" >/dev/null 2>&1
  openssl pkey -in "$TMP_DIR/auth.ed" -pubout -out "$TMP_DIR/auth.ed.pub" >/dev/null 2>&1

  local svc
  for svc in users-service workflows-service jobs-service notifications-service analytics-service postgres redis clickhouse kafka meilisearch; do
    generate_cert "$svc" "$svc" "DNS:$svc,DNS:$svc.$NAMESPACE,DNS:$svc.$NAMESPACE.svc,DNS:$svc.$NAMESPACE.svc.cluster.local,IP:127.0.0.1"
  done
  generate_cert client chronoverse-client "DNS:client"

  create_file_secret chronoverse-auth \
    --from-file=auth.ed="$TMP_DIR/auth.ed" \
    --from-file=auth.ed.pub="$TMP_DIR/auth.ed.pub"
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
  if [ "$HOSTPATH_FALLBACK" = true ]; then
    info "No default StorageClass found; hostPath fallback explicitly allowed"
    return
  fi
  if [ "$DRY_RUN" = true ]; then
    info "No default StorageClass found during dry-run; production apply will require --storage-class or --hostpath-fallback"
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
  kubectl_cmd -n "$NAMESPACE" delete job "${jobs[@]}" --ignore-not-found --wait=false >/dev/null
}

create_data_secrets
if [ "$MODE" = "production" ]; then
  create_production_tls_secrets
fi
check_storage

if [ -n "$STORAGE_CLASS" ]; then
  PATCH_DIR="$(mktemp -d "$ROOT_DIR/.k8s-setup.XXXXXX")"
  mkdir -p "$PATCH_DIR"
  cat > "$PATCH_DIR/kustomization.yaml" <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
- ../infra/k8s/overlays/$MODE
patches:
- target:
    kind: PersistentVolumeClaim
  patch: |-
    - op: add
      path: /spec/storageClassName
      value: $STORAGE_CLASS
EOF
  KUSTOMIZE_DIR="$PATCH_DIR"
fi

if [ "$SKIP_APPLY" = true ]; then
  info "Skipping manifest apply"
  exit 0
fi

delete_bootstrap_jobs

if [ "$DRY_RUN" = true ]; then
  info "Running dry-run apply for $MODE"
  if [ -n "$STORAGE_CLASS" ]; then
    kubectl_cmd kustomize --load-restrictor=LoadRestrictionsNone "$KUSTOMIZE_DIR" | kubectl_cmd apply --dry-run=client --validate=false -f -
  else
    kubectl_cmd apply --dry-run=client --validate=false -k "$KUSTOMIZE_DIR"
  fi
else
  info "Applying $MODE overlay"
  if [ -n "$STORAGE_CLASS" ]; then
    kubectl_cmd kustomize --load-restrictor=LoadRestrictionsNone "$KUSTOMIZE_DIR" | kubectl_cmd apply -f -
  else
    kubectl_cmd apply -k "$KUSTOMIZE_DIR"
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

Open the dashboard/API locally:
  kubectl ${KUBECTL_CONTEXT_PREFIX}-n $NAMESPACE port-forward svc/nginx 8080:80
  http://localhost:8080

Open LGTM locally:
  kubectl ${KUBECTL_CONTEXT_PREFIX}-n $NAMESPACE port-forward svc/lgtm 3000:3000
  http://localhost:3000

Check registered runtimes:
  kubectl ${KUBECTL_CONTEXT_PREFIX}-n $NAMESPACE exec postgres-0 -- sh -c 'PGPASSWORD="\$POSTGRES_PASSWORD" psql -U "\$POSTGRES_USER" -d "\$POSTGRES_DB" -c "select id,node_name,docker_endpoint,status from runtime_nodes;"'
EOF
fi
