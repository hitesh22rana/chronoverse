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

mkdir -p "$issuer_dir"

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
# Generate new Ed25519 keypair (overwrite current).
openssl genpkey -algorithm ED25519 -outform pem -out "$issuer_dir/auth.ed" 2>/dev/null
openssl pkey -in "$issuer_dir/auth.ed" -pubout -out "$issuer_dir/auth.ed.pub" 2>/dev/null
chmod 440 "$issuer_dir/auth.ed"
chown 100:101 "$issuer_dir/auth.ed" 2>/dev/null || chgrp 101 "$issuer_dir/auth.ed" 2>/dev/null || true
chmod 444 "$issuer_dir/auth.ed.pub"

new_kid="$issuer:$(date +%Y%m%d)-$(openssl rand -hex 2)"
echo "🆕 New kid: $new_kid"

# Ensure trusted bundle exists.
if [ ! -f "$trusted" ]; then
  echo "{}" > "$trusted"
fi

# Update bundle: preserve old kid as inline PEM (if it existed and pointed to file), add new kid.
tmp=$(mktemp)
if [ -n "$old_kid" ] && [ -n "$old_pub_pem" ]; then
  # Keep old kid but pin its pub to inline PEM so old tokens remain valid during grace period.
  jq --arg kid "$old_kid" --arg pem "$old_pub_pem" \
    '.[$kid].pub = $pem' "$trusted" > "$tmp" && mv "$tmp" "$trusted"
  echo "📌 Preserved old kid $old_kid as inline PEM for grace period"
fi

# Add new kid pointing to file path.
jq --arg kid "$new_kid" --arg iss "$issuer" --arg pub "$issuer/auth.ed.pub" \
  '. + {($kid): {"iss": $iss, "pub": $pub}}' "$trusted" > "$tmp" && mv "$tmp" "$trusted"
chmod 444 "$trusted"

echo "✅ Trusted bundle updated: $trusted"
echo "   Current entries for $issuer:"
jq --arg iss "$issuer" 'to_entries[] | select(.value.iss == $iss) | "\(.key): \(.value.pub | split("\n")[0])"' "$trusted"

cat <<EOF

Next steps:
  1. Distribute the updated trusted.json to verifiers and restart verifiers before the signer (Compose: docker compose up -d --force-recreate)
  2. After grace period (15m), prune the old kid and restart verifiers again:
       jq 'del(."$old_kid")' $trusted > $trusted.tmp && mv $trusted.tmp $trusted && docker compose up -d --force-recreate
     (only if old_kid was set; verifiers cache the bundle in memory and must reload to drop the old kid)

EOF
