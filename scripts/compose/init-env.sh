#!/bin/sh
set -eu

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
env_file="${1:-$root_dir/.env}"
tmp_file=$(mktemp "${env_file}.XXXXXX")
trap 'rm -f "$tmp_file"' EXIT HUP INT TERM

umask 077
touch "$env_file"

crypto_secret=$(openssl rand -hex 16)
csrf_secret=$(openssl rand -hex 32)

awk -v crypto="$crypto_secret" -v csrf="$csrf_secret" '
  function value(line) {
    return substr(line, index(line, "=") + 1)
  }
  function nonempty(candidate, trimmed, first, last) {
    trimmed = candidate
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", trimmed)
    first = substr(trimmed, 1, 1)
    last = substr(trimmed, length(trimmed), 1)
    if ((first == "\"" && last == "\"") || (first == "\047" && last == "\047")) {
      trimmed = substr(trimmed, 2, length(trimmed) - 2)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", trimmed)
    }
    return trimmed != ""
  }
  {
    lines[NR] = $0
    if ($0 ~ /^CRYPTO_SECRET=/ && nonempty(value($0))) crypto_value = value($0)
    if ($0 ~ /^SERVER_CSRF_HMAC_SECRET=/ && nonempty(value($0))) csrf_value = value($0)
  }
  END {
    for (i = 1; i <= NR; i++) {
      if (lines[i] ~ /^CRYPTO_SECRET=/) {
        if (!crypto_written++) print "CRYPTO_SECRET=" (crypto_value != "" ? crypto_value : crypto)
      } else if (lines[i] ~ /^SERVER_CSRF_HMAC_SECRET=/) {
        if (!csrf_written++) print "SERVER_CSRF_HMAC_SECRET=" (csrf_value != "" ? csrf_value : csrf)
      } else {
        print lines[i]
      }
    }
    if (!crypto_written) print "CRYPTO_SECRET=" crypto
    if (!csrf_written) print "SERVER_CSRF_HMAC_SECRET=" csrf
  }
' "$env_file" > "$tmp_file"

chmod 600 "$tmp_file"
mv "$tmp_file" "$env_file"
trap - EXIT HUP INT TERM
