#!/bin/sh
set -eu

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
env_file="${1:-$root_dir/.env}"

umask 077
touch "$env_file"

compose_environment=$(
  unset CRYPTO_SECRET SERVER_CSRF_HMAC_SECRET
  printf '%s\n' \
    'services:' \
    '  probe:' \
    '    image: scratch' \
    '    environment:' \
    '      CRYPTO_SECRET: ${CRYPTO_SECRET-}' \
    '      SERVER_CSRF_HMAC_SECRET: ${SERVER_CSRF_HMAC_SECRET-}' \
    | docker compose --env-file "$env_file" -f - config --environment
)

if ! printf '%s\n' "$compose_environment" | grep -q '^CRYPTO_SECRET=.'; then
  printf '\nCRYPTO_SECRET=%s\n' "$(openssl rand -hex 16)" >> "$env_file"
fi
if ! printf '%s\n' "$compose_environment" | grep -q '^SERVER_CSRF_HMAC_SECRET=.'; then
  printf '\nSERVER_CSRF_HMAC_SECRET=%s\n' "$(openssl rand -hex 32)" >> "$env_file"
fi

chmod 600 "$env_file"
