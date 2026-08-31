#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="chronoverse"
CONTEXT=""
TIMEOUT="5m"
CERT_DAYS="3650"
TMP_DIR=""

usage() {
  cat <<'EOF'
Usage: scripts/k8s/rotate-docker-proxy-certs.sh [options]

Rotates the dedicated Docker proxy CA, server identity, and three client-role
identities with an overlapping trust bundle and staged Kubernetes rollouts.
Run during a maintenance window because a single-node DaemonSet has a brief
proxy interruption while its hostPort pod restarts.

Options:
  --context <name>       kubectl context to use.
  --namespace <name>     Kubernetes namespace (default: chronoverse).
  --timeout <duration>   rollout status timeout (default: 5m).
  --cert-days <days>     certificate lifetime (default: 3650).
  -h, --help             Show this help.
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

info() {
  echo "==> $*"
}

kubectl_cmd() {
  if [ -n "$CONTEXT" ]; then
    kubectl --context "$CONTEXT" -n "$NAMESPACE" "$@"
  else
    kubectl -n "$NAMESPACE" "$@"
  fi
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --context)
      CONTEXT="${2:-}"
      shift 2
      ;;
    --namespace)
      NAMESPACE="${2:-}"
      shift 2
      ;;
    --timeout)
      TIMEOUT="${2:-}"
      shift 2
      ;;
    --cert-days)
      CERT_DAYS="${2:-}"
      shift 2
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

command -v kubectl >/dev/null 2>&1 || die "kubectl is required"
command -v openssl >/dev/null 2>&1 || die "openssl is required"
case "$CERT_DAYS" in
  ''|*[!0-9]*) die "--cert-days must be a positive integer" ;;
esac
[ "$CERT_DAYS" -gt 0 ] || die "--cert-days must be greater than zero"
[ -n "$NAMESPACE" ] || die "--namespace must not be empty"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

for secret in docker-proxy-ca docker-proxy-server \
  docker-proxy-client-runtime-agent docker-proxy-client-workflow-worker \
  docker-proxy-client-execution-worker; do
  kubectl_cmd get secret "$secret" >/dev/null 2>&1 || \
    die "missing $secret; run scripts/k8s/setup.sh before rotation"
done

kubectl_cmd get secret docker-proxy-ca -o jsonpath='{.data.ca\.crt}' | \
  openssl base64 -d -A > "$TMP_DIR/old-ca.crt"
[ -s "$TMP_DIR/old-ca.crt" ] || die "docker-proxy-ca does not contain ca.crt"

info "Generating the replacement Docker proxy PKI"
openssl genrsa -out "$TMP_DIR/new-ca.key" 4096 >/dev/null 2>&1
openssl req -x509 -new -nodes -key "$TMP_DIR/new-ca.key" -sha256 \
  -days "$CERT_DAYS" -out "$TMP_DIR/new-ca.crt" -subj "/CN=docker-proxy-ca" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" >/dev/null 2>&1

openssl genrsa -out "$TMP_DIR/server.key" 4096 >/dev/null 2>&1
openssl req -new -key "$TMP_DIR/server.key" -out "$TMP_DIR/server.csr" \
  -subj "/CN=docker-proxy" >/dev/null 2>&1
cat > "$TMP_DIR/server-ext.cnf" <<'EOF'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:docker-proxy,DNS:docker-proxy.chronoverse,DNS:docker-proxy.chronoverse.svc,IP:127.0.0.1
EOF
openssl x509 -req -in "$TMP_DIR/server.csr" -CA "$TMP_DIR/new-ca.crt" \
  -CAkey "$TMP_DIR/new-ca.key" -CAcreateserial -out "$TMP_DIR/server.crt" \
  -days "$CERT_DAYS" -extfile "$TMP_DIR/server-ext.cnf" >/dev/null 2>&1
cat "$TMP_DIR/server.crt" "$TMP_DIR/server.key" > "$TMP_DIR/server.pem"

for role in runtime-agent workflow-worker execution-worker; do
  openssl genrsa -out "$TMP_DIR/$role.key" 4096 >/dev/null 2>&1
  openssl req -new -key "$TMP_DIR/$role.key" -out "$TMP_DIR/$role.csr" \
    -subj "/CN=docker-proxy-client-$role" >/dev/null 2>&1
  cat > "$TMP_DIR/$role-ext.cnf" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth
subjectAltName=DNS:docker-proxy-client-$role
EOF
  openssl x509 -req -in "$TMP_DIR/$role.csr" -CA "$TMP_DIR/new-ca.crt" \
    -CAkey "$TMP_DIR/new-ca.key" -CAserial "$TMP_DIR/new-ca.srl" \
    -out "$TMP_DIR/$role.crt" -days "$CERT_DAYS" \
    -extfile "$TMP_DIR/$role-ext.cnf" >/dev/null 2>&1
done
cat "$TMP_DIR/old-ca.crt" "$TMP_DIR/new-ca.crt" > "$TMP_DIR/ca-bundle.crt"

apply_secret() {
  local name="$1"
  shift
  kubectl_cmd create secret generic "$name" "$@" --dry-run=client -o yaml | \
    kubectl_cmd apply -f - >/dev/null
}

restart_proxy() {
  kubectl_cmd rollout restart daemonset/docker-proxy >/dev/null
  kubectl_cmd rollout status daemonset/docker-proxy --timeout="$TIMEOUT"
}

restart_clients() {
  kubectl_cmd rollout restart deployment/workflow-worker deployment/execution-worker >/dev/null
  kubectl_cmd rollout status deployment/workflow-worker --timeout="$TIMEOUT"
  kubectl_cmd rollout status deployment/execution-worker --timeout="$TIMEOUT"
}

info "Stage 1/4: installing overlapping old and new trust roots"
apply_secret docker-proxy-ca --from-file=ca.crt="$TMP_DIR/ca-bundle.crt"
restart_proxy

info "Stage 2/4: rotating each least-privilege client identity"
apply_secret docker-proxy-client-runtime-agent \
  --from-file=tls.crt="$TMP_DIR/runtime-agent.crt" \
  --from-file=tls.key="$TMP_DIR/runtime-agent.key"
apply_secret docker-proxy-client-workflow-worker \
  --from-file=tls.crt="$TMP_DIR/workflow-worker.crt" \
  --from-file=tls.key="$TMP_DIR/workflow-worker.key"
apply_secret docker-proxy-client-execution-worker \
  --from-file=tls.crt="$TMP_DIR/execution-worker.crt" \
  --from-file=tls.key="$TMP_DIR/execution-worker.key"
restart_clients
restart_proxy

info "Stage 3/4: rotating the proxy server identity"
apply_secret docker-proxy-server --from-file=server.pem="$TMP_DIR/server.pem"
restart_proxy

info "Stage 4/4: removing the retired trust root"
apply_secret docker-proxy-ca --from-file=ca.crt="$TMP_DIR/new-ca.crt"
restart_proxy
restart_clients

info "Docker proxy certificate rotation completed"
