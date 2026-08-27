#!/bin/sh
set -eu

root_dir="${1:-/docker-proxy-certs}"
legacy_dir="${2:-/certs/docker-proxy}"
cert_days="${DOCKER_PROXY_CERT_DAYS:-365}"
rotate="${DOCKER_PROXY_ROTATE_CERTS:-false}"

case "$root_dir" in
  /docker-proxy-certs|*/docker-proxy-certs) ;;
  *) echo "refusing unsafe Docker proxy certificate directory: $root_dir" >&2; exit 1 ;;
esac
case "$cert_days" in
  ''|*[!0-9]*) echo "DOCKER_PROXY_CERT_DAYS must be a positive integer" >&2; exit 1 ;;
esac
[ "$cert_days" -gt 0 ] || { echo "DOCKER_PROXY_CERT_DAYS must be greater than zero" >&2; exit 1; }

complete=true
for file in \
  issuer/ca.crt issuer/ca.key \
  server/ca.crt server/server.pem server/token \
  runtime-agent/ca.crt runtime-agent/client.crt runtime-agent/client.key runtime-agent/token \
  workflow-worker/ca.crt workflow-worker/client.crt workflow-worker/client.key workflow-worker/token \
  execution-worker/ca.crt execution-worker/client.crt execution-worker/client.key execution-worker/token; do
  [ -s "$root_dir/$file" ] || complete=false
done

if [ "$rotate" = true ] || [ "$complete" != true ]; then
  echo "Generating isolated Docker proxy mTLS identities"
  staging_dir="$root_dir/.staging"
  rm -rf "$staging_dir"
  mkdir -p "$staging_dir/issuer" "$staging_dir/server"
  for role in runtime-agent workflow-worker execution-worker; do
    mkdir -p "$staging_dir/$role"
  done

  openssl genrsa -out "$staging_dir/issuer/ca.key" 4096 >/dev/null 2>&1
  openssl req -x509 -new -nodes -key "$staging_dir/issuer/ca.key" -sha256 \
    -days "$cert_days" -out "$staging_dir/issuer/ca.crt" -subj "/CN=docker-proxy-ca" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" >/dev/null 2>&1

  openssl genrsa -out "$staging_dir/server/server.key" 4096 >/dev/null 2>&1
  openssl req -new -key "$staging_dir/server/server.key" \
    -out "$staging_dir/server/server.csr" -subj "/CN=docker-proxy" >/dev/null 2>&1
  cat > "$staging_dir/server/server-ext.cnf" <<'EOF'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:docker-proxy,DNS:docker-proxy.chronoverse,DNS:docker-proxy.chronoverse.svc,DNS:docker-proxy.chronoverse.svc.cluster.local,IP:127.0.0.1
EOF
  openssl x509 -req -in "$staging_dir/server/server.csr" \
    -CA "$staging_dir/issuer/ca.crt" -CAkey "$staging_dir/issuer/ca.key" \
    -CAcreateserial -out "$staging_dir/server/server.crt" -days "$cert_days" \
    -extfile "$staging_dir/server/server-ext.cnf" >/dev/null 2>&1
  cat "$staging_dir/server/server.crt" "$staging_dir/server/server.key" > "$staging_dir/server/server.pem"

  for role in runtime-agent workflow-worker execution-worker; do
    openssl genrsa -out "$staging_dir/$role/client.key" 4096 >/dev/null 2>&1
    openssl req -new -key "$staging_dir/$role/client.key" \
      -out "$staging_dir/$role/client.csr" \
      -subj "/CN=docker-proxy-client-$role" >/dev/null 2>&1
    cat > "$staging_dir/$role/client-ext.cnf" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth
subjectAltName=DNS:docker-proxy-client-$role
EOF
    openssl x509 -req -in "$staging_dir/$role/client.csr" \
      -CA "$staging_dir/issuer/ca.crt" -CAkey "$staging_dir/issuer/ca.key" \
      -CAserial "$staging_dir/issuer/ca.srl" -out "$staging_dir/$role/client.crt" \
      -days "$cert_days" -extfile "$staging_dir/$role/client-ext.cnf" >/dev/null 2>&1
  done

  token="$(openssl rand -hex 32)"
  cp "$staging_dir/issuer/ca.crt" "$staging_dir/server/ca.crt"
  for role in runtime-agent workflow-worker execution-worker; do
    cp "$staging_dir/issuer/ca.crt" "$staging_dir/$role/ca.crt"
    printf '%s\n' "$token" > "$staging_dir/$role/token"
  done
  printf '%s\n' "$token" > "$staging_dir/server/token"

  rm -f "$staging_dir/issuer/ca.srl" \
    "$staging_dir/server/server.csr" "$staging_dir/server/server-ext.cnf" \
    "$staging_dir/server/server.crt" "$staging_dir/server/server.key"
  for role in runtime-agent workflow-worker execution-worker; do
    rm -f "$staging_dir/$role/client.csr" "$staging_dir/$role/client-ext.cnf"
  done

  for dir in issuer server runtime-agent workflow-worker execution-worker; do
    rm -rf "${root_dir:?}/$dir"
    mv "$staging_dir/$dir" "$root_dir/$dir"
  done
  rmdir "$staging_dir"
fi

# HAProxy is root; application images use app UID/GID 100:101. Exact role
# mounts keep the issuer, server, and other clients' private keys inaccessible.
chown -R 0:0 "$root_dir/issuer" "$root_dir/server"
chmod 0500 "$root_dir/issuer" "$root_dir/server"
chmod 0400 "$root_dir/issuer/ca.crt" "$root_dir/issuer/ca.key"
chmod 0444 "$root_dir/server/ca.crt"
chmod 0400 "$root_dir/server/server.pem" "$root_dir/server/token"
for role in runtime-agent workflow-worker execution-worker; do
  chown -R 100:101 "$root_dir/$role"
  chmod 0500 "$root_dir/$role"
  chmod 0400 "$root_dir/$role/ca.crt" "$root_dir/$role/client.crt" \
    "$root_dir/$role/client.key" "$root_dir/$role/token"
done

if [ -d "$legacy_dir" ]; then
  case "$legacy_dir" in
    /certs/docker-proxy|*/certs/docker-proxy) rm -rf "$legacy_dir" ;;
    *) echo "refusing unsafe legacy certificate directory: $legacy_dir" >&2; exit 1 ;;
  esac
fi

echo "Docker proxy identities are isolated and ready"
