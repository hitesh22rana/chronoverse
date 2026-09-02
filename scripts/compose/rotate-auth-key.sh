#!/bin/sh
set -eu

# Rotate a single issuer's Ed25519 key and update the trusted bundle.
# Usage: ./scripts/compose/rotate-auth-key.sh <issuer>
# Example: ./scripts/compose/rotate-auth-key.sh server

issuer="${1:-}"
if [ -z "$issuer" ]; then
  echo "usage: $0 <issuer>" >&2
  echo "  issuer must be one of: server users-service workflows-service jobs-service notifications-service analytics-service scheduling-worker workflow-worker execution-worker runtime-agent joblogs-processor analytics-processor outbox-relay" >&2
  exit 1
fi

# Validate issuer
case "$issuer" in
  server|users-service|workflows-service|jobs-service|notifications-service|analytics-service|scheduling-worker|workflow-worker|execution-worker|runtime-agent|joblogs-processor|analytics-processor|outbox-relay) ;;
  *) echo "unknown issuer: $issuer" >&2; exit 1 ;;
esac

command -v openssl >/dev/null 2>&1 || { echo "openssl is required" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
issuers_dir="$root_dir/certs/issuers"
issuer_dir="$issuers_dir/$issuer"
trusted="$issuers_dir/trusted.json"

# Decide whether host can write directly. The initializer creates files as 100:101 0440
# (and trusted.json 0444) inside a root-owned bind mount, so a normal operator UID
# (e.g. 1000) cannot truncate them. In that case delegate the mutation to a
# short-lived privileged helper container that mounts ./certs rw as root.
use_docker=0
if [ -d "$issuers_dir" ] && [ ! -w "$issuers_dir" ]; then
  use_docker=1
fi
if [ -e "$issuer_dir/auth.ed" ] && [ ! -w "$issuer_dir/auth.ed" ]; then
  use_docker=1
fi
if [ -e "$trusted" ] && [ ! -w "$trusted" ]; then
  use_docker=1
fi
# Probe directory writability for creating temp files (mv needs directory write)
if [ -d "$issuers_dir" ] && ! touch "$issuers_dir/.wtest" 2>/dev/null; then
  use_docker=1
else
  rm -f "$issuers_dir/.wtest" 2>/dev/null || true
fi
if [ "$use_docker" -eq 1 ] && ! command -v docker >/dev/null 2>&1; then
  echo "rotation needs write access to $issuers_dir (owned 100:101 0440); run with sudo or install docker for helper container" >&2
  exit 1
fi

run_in_certs() {
  # run_in_certs <sh-script>
  # Executes the script inside an alpine helper with /certs mounted rw, installing
  # openssl+jq on first use. Falls back to host sh when docker is not needed.
  if [ "$use_docker" -eq 1 ]; then
    docker run --rm -v "$root_dir/certs:/certs:rw" alpine:3.22 sh -c "apk add --no-cache openssl jq >/dev/null 2>&1; $1"
  else
    sh -c "$1"
  fi
}

# Capture old public key PEM before rotation (for grace period).
old_pub_pem=""
old_kid=""
if [ -f "$issuer_dir/auth.ed.pub" ] && [ -f "$trusted" ]; then
  old_pub_pem=$(cat "$issuer_dir/auth.ed.pub")
  # Find the kid that currently points to this issuer's file path.
  old_kid=$(jq -r --arg iss "$issuer" --arg pub "$issuer/auth.ed.pub" '
    to_entries[] | select(.value.iss == $iss and .value.pub == $pub) | .key
  ' "$trusted" 2>/dev/null | head -n1 || true)
fi

echo "🔐 Rotating key for $issuer..."
if [ "$use_docker" -eq 1 ]; then
  run_in_certs "mkdir -p /certs/issuers/$issuer"
else
  mkdir -p "$issuer_dir"
fi
# Ensure trusted bundle exists.
if [ ! -f "$trusted" ]; then
  if [ "$use_docker" -eq 1 ]; then
    run_in_certs "printf '{}\n' > /certs/issuers/trusted.json && chmod 444 /certs/issuers/trusted.json"
  else
    printf '{}\n' > "$trusted"
    chmod 444 "$trusted"
  fi
fi

# Update bundle: preserve old kid as inline PEM (if it existed and pointed to file), add new kid.
if [ -n "$old_kid" ] && [ -n "$old_pub_pem" ]; then
  if [ "$use_docker" -eq 1 ]; then
    # Escape pem for passing to container jq
    tmp=$(mktemp)
    printf '%s' "$old_pub_pem" > "$tmp"
    # Use host jq to produce escaped json, then container jq to update
    # Simpler: do the jq inside container by mounting tmp as well via stdin
    # We pass pem via environment inside container using base64 to avoid quoting issues
    b64=$(printf '%s' "$old_pub_pem" | base64 | tr -d '\n')
    run_in_certs "
      set -eu
      chmod u+w /certs/issuers 2>/dev/null || true
      chmod u+w /certs/issuers/trusted.json 2>/dev/null || true
      pem=\$(printf '%s' '$b64' | base64 -d)
      tmp=\$(mktemp /certs/issuers/tmp.XXXXXX)
      jq --arg kid '$old_kid' --arg pem \"\$pem\" '.[\$kid].pub = \$pem' /certs/issuers/trusted.json > \"\$tmp\" && mv \"\$tmp\" /certs/issuers/trusted.json
      chmod 444 /certs/issuers/trusted.json
    "
    rm -f "$tmp"
  else
    tmp=$(mktemp "$issuers_dir/tmp.XXXXXX")
    jq --arg kid "$old_kid" --arg pem "$old_pub_pem" \
      '.[$kid].pub = $pem' "$trusted" > "$tmp" && mv "$tmp" "$trusted"
    chmod 444 "$trusted"
  fi
  echo "📌 Preserved old kid $old_kid as inline PEM for grace period"
fi

# Generate new Ed25519 keypair (overwrite current).
if [ "$use_docker" -eq 1 ]; then
  run_in_certs "
    set -eu
    chmod u+w /certs/issuers 2>/dev/null || true
    chmod u+w /certs/issuers/$issuer 2>/dev/null || true
    tmp_priv=\$(mktemp /certs/issuers/$issuer/tmp.XXXXXX)
    tmp_pub=\$(mktemp /certs/issuers/$issuer/tmp.XXXXXX)
    openssl genpkey -algorithm ED25519 -outform pem -out \$tmp_priv 2>/dev/null
    openssl pkey -in \$tmp_priv -pubout -out \$tmp_pub 2>/dev/null
    chmod 440 \$tmp_priv
    chmod 444 \$tmp_pub
    chmod u+w /certs/issuers/$issuer/auth.ed 2>/dev/null || true
    chmod u+w /certs/issuers/$issuer/auth.ed.pub 2>/dev/null || true
    if [ -f /certs/issuers/$issuer/auth.ed ]; then if ! cp /certs/issuers/$issuer/auth.ed /certs/issuers/$issuer/.auth.ed.bak; then echo "backup of old private key failed" >&2; rm -f \$tmp_priv \$tmp_pub; exit 1; fi; fi
    if ! mv \$tmp_priv /certs/issuers/$issuer/auth.ed; then rm -f \$tmp_pub; rm -f /certs/issuers/$issuer/.auth.ed.bak 2>/dev/null || true; exit 1; fi
    if ! mv \$tmp_pub /certs/issuers/$issuer/auth.ed.pub; then echo "public key install failed, restoring private key" >&2; if [ -f /certs/issuers/$issuer/.auth.ed.bak ]; then mv /certs/issuers/$issuer/.auth.ed.bak /certs/issuers/$issuer/auth.ed; chmod 440 /certs/issuers/$issuer/auth.ed; chown 100:101 /certs/issuers/$issuer/auth.ed 2>/dev/null || chgrp 101 /certs/issuers/$issuer/auth.ed 2>/dev/null || true; else rm -f /certs/issuers/$issuer/auth.ed; echo "no backup available, removed partial private key" >&2; fi; exit 1; fi
    rm -f /certs/issuers/$issuer/.auth.ed.bak
    chmod 440 /certs/issuers/$issuer/auth.ed
    chown 100:101 /certs/issuers/$issuer/auth.ed 2>/dev/null || chgrp 101 /certs/issuers/$issuer/auth.ed 2>/dev/null || true
    chmod 444 /certs/issuers/$issuer/auth.ed.pub
  "
else
  chmod u+w "$issuers_dir" 2>/dev/null || true
  chmod u+w "$issuer_dir" 2>/dev/null || true
  tmp_priv=$(mktemp "$issuer_dir/tmp.XXXXXX")
  tmp_pub=$(mktemp "$issuer_dir/tmp.XXXXXX")
  openssl genpkey -algorithm ED25519 -outform pem -out "$tmp_priv" 2>/dev/null || { rm -f "$tmp_priv" "$tmp_pub"; echo "openssl genpkey failed (Ed25519 not supported by host openssl; install openssl with Ed25519 or use docker helper)" >&2; exit 1; }
  openssl pkey -in "$tmp_priv" -pubout -out "$tmp_pub" 2>/dev/null || { rm -f "$tmp_priv" "$tmp_pub"; echo "openssl pkey failed" >&2; exit 1; }
  chmod 440 "$tmp_priv"
  chmod 444 "$tmp_pub"
  chmod u+w "$issuer_dir/auth.ed" 2>/dev/null || true
  chmod u+w "$issuer_dir/auth.ed.pub" 2>/dev/null || true
  if [ -f "$issuer_dir/auth.ed" ]; then if ! cp "$issuer_dir/auth.ed" "$issuer_dir/.auth.ed.bak"; then echo "backup of old private key failed" >&2; rm -f "$tmp_priv" "$tmp_pub"; exit 1; fi; fi
  if ! mv "$tmp_priv" "$issuer_dir/auth.ed"; then rm -f "$tmp_pub"; rm -f "$issuer_dir/.auth.ed.bak" 2>/dev/null || true; exit 1; fi
  if ! mv "$tmp_pub" "$issuer_dir/auth.ed.pub"; then echo "public key install failed, restoring private key" >&2; if [ -f "$issuer_dir/.auth.ed.bak" ]; then mv "$issuer_dir/.auth.ed.bak" "$issuer_dir/auth.ed"; chmod 440 "$issuer_dir/auth.ed"; chown 100:101 "$issuer_dir/auth.ed" 2>/dev/null || chgrp 101 "$issuer_dir/auth.ed" 2>/dev/null || true; else rm -f "$issuer_dir/auth.ed"; echo "no backup available, removed partial private key" >&2; fi; exit 1; fi
  rm -f "$issuer_dir/.auth.ed.bak"
  chmod 440 "$issuer_dir/auth.ed"
  chown 100:101 "$issuer_dir/auth.ed" 2>/dev/null || chgrp 101 "$issuer_dir/auth.ed" 2>/dev/null || true
  chmod 444 "$issuer_dir/auth.ed.pub"
fi

new_kid="$issuer:$(date +%Y%m%d)-$(openssl rand -hex 2)"
echo "🆕 New kid: $new_kid"

# Add new kid pointing to file path.
if [ "$use_docker" -eq 1 ]; then
  run_in_certs "
    set -eu
    chmod u+w /certs/issuers 2>/dev/null || true
    chmod u+w /certs/issuers/trusted.json 2>/dev/null || true
    tmp=\$(mktemp /certs/issuers/tmp.XXXXXX)
    jq --arg kid '$new_kid' --arg iss '$issuer' --arg pub '$issuer/auth.ed.pub' '. + {(\$kid): {\"iss\": \$iss, \"pub\": \$pub}}' /certs/issuers/trusted.json > \"\$tmp\" && mv \"\$tmp\" /certs/issuers/trusted.json
    chmod 444 /certs/issuers/trusted.json
  "
else
  tmp=$(mktemp "$issuers_dir/tmp.XXXXXX")
  jq --arg kid "$new_kid" --arg iss "$issuer" --arg pub "$issuer/auth.ed.pub" \
    '. + {($kid): {"iss": $iss, "pub": $pub}}' "$trusted" > "$tmp" && mv "$tmp" "$trusted"
  chmod 444 "$trusted"
fi

echo "✅ Trusted bundle updated: $trusted"
echo "   Current entries for $issuer:"
jq --arg iss "$issuer" 'to_entries[] | select(.value.iss == $iss) | "\(.key): \(.value.pub | split("\n")[0])"' "$trusted"

# Pick compose file for restart instructions (repo has compose.dev.yaml / compose.prod.yaml, not compose.yaml)
compose_file="compose.prod.yaml"
if [ -n "${COMPOSE_FILE:-}" ]; then
  compose_file="$COMPOSE_FILE"
elif [ ! -f "$root_dir/compose.prod.yaml" ] && [ -f "$root_dir/compose.dev.yaml" ]; then
  compose_file="compose.dev.yaml"
fi
# Verifiers are every issuer except the rotated one; the signer is the issuer itself.
# For Compose, the service name matches the issuer (database-migration reuses server).
all_issuers="server users-service workflows-service jobs-service notifications-service analytics-service scheduling-worker workflow-worker execution-worker runtime-agent joblogs-processor analytics-processor outbox-relay"
verifiers=""
for iss in $all_issuers; do
  if [ "$iss" != "$issuer" ]; then
    # Map issuer to compose service name (they match)
    verifiers="$verifiers $iss"
  fi
done
# database-migration is not an issuer but verifies; include if rotating server
if [ "$issuer" = "server" ]; then
  verifiers="$verifiers"
fi
# Trim leading space
verifiers=$(printf '%s' "$verifiers" | sed 's/^ //')

cat <<EOF

Next steps (verifier-first):
  1. Restart verifiers with the new dual-key bundle, then the signer:
       docker compose -f $compose_file up -d --no-deps --force-recreate $verifiers
       docker compose -f $compose_file up -d --no-deps --force-recreate $issuer
     (if you use COMPOSE_FILE env, omit -f; for dev use -f compose.dev.yaml)
  2. After grace period (15m, token expiry), prune the old kid and restart verifiers again:
       jq 'del(."$old_kid")' $trusted > $trusted.tmp && mv $trusted.tmp $trusted && docker compose -f $compose_file up -d --no-deps --force-recreate $verifiers
     (only if old_kid was set; verifiers cache the bundle in memory by Auth.New and must reload to drop the old kid)
     When rotation used the helper container, the jq above may need the same helper:
       docker run --rm -v "$root_dir/certs:/certs:rw" alpine:3.22 sh -c "apk add --no-cache jq >/dev/null 2>&1; tmp=\$(mktemp /certs/issuers/tmp.XXXXXX); jq 'del(.\"$old_kid\")' /certs/issuers/trusted.json > \$tmp && mv \$tmp /certs/issuers/trusted.json && chmod 444 /certs/issuers/trusted.json"
       docker compose -f $compose_file up -d --no-deps --force-recreate $verifiers

EOF