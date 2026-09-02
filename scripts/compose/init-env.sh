#!/bin/sh
set -eu

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
env_file="${1:-$root_dir/.env}"
docker_path=$(command -v docker)

if [ "${CRYPTO_SECRET+x}" = x ] || [ "${SERVER_CSRF_HMAC_SECRET+x}" = x ]; then
  echo "unset CRYPTO_SECRET and SERVER_CSRF_HMAC_SECRET before initializing .env" >&2
  exit 1
fi

umask 077
touch "$env_file"
chmod 600 "$env_file"

compose_environment=$(
  unset CRYPTO_SECRET SERVER_CSRF_HMAC_SECRET
  printf '%s\n' \
    'services:' \
    '  probe:' \
    '    image: scratch' \
    '    environment:' \
    '      CRYPTO_SECRET: ${CRYPTO_SECRET-}' \
    '      SERVER_CSRF_HMAC_SECRET: ${SERVER_CSRF_HMAC_SECRET-}' \
    | env -i "$docker_path" compose --env-file "$env_file" -f - config --environment
)

if ! printf '%s\n' "$compose_environment" | grep -q '^CRYPTO_SECRET=.'; then
  crypto_secret=$(openssl rand -hex 16)
  [ -n "$crypto_secret" ] || { echo "OpenSSL did not generate CRYPTO_SECRET" >&2; exit 1; }
  printf '\nCRYPTO_SECRET=%s\n' "$crypto_secret" >> "$env_file"
fi
if ! printf '%s\n' "$compose_environment" | grep -q '^SERVER_CSRF_HMAC_SECRET=.'; then
  csrf_secret=$(openssl rand -hex 32)
  [ -n "$csrf_secret" ] || { echo "OpenSSL did not generate SERVER_CSRF_HMAC_SECRET" >&2; exit 1; }
  printf '\nSERVER_CSRF_HMAC_SECRET=%s\n' "$csrf_secret" >> "$env_file"
fi
