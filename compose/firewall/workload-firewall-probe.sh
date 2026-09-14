#!/bin/sh
# Firewall health probe: the exact installed rules must exist in the backend
# Docker uses (nftables or legacy — invisible to each other). Succeeds in
# exactly one backend view; BOTH views missing the rules means unhealthy.
set -eu

SUBNET="${WORKLOAD_SUBNET:?set WORKLOAD_SUBNET}"
IF="${WORKLOAD_BRIDGE_IF:-chronoverse-br}"
CHAIN="CHRONOVERSE-WORKLOAD"
CHAIN_IN="CHRONOVERSE-WORKLOAD-IN"

healthy() {
    "$1" -C DOCKER-USER -j "$CHAIN" 2>/dev/null \
    && "$1" -C "$CHAIN" -s "$SUBNET" -i "$IF" -j ACCEPT 2>/dev/null \
    && "$1" -C INPUT -j "$CHAIN_IN" 2>/dev/null \
    && "$1" -C "$CHAIN_IN" -s "$SUBNET" -m conntrack --ctstate NEW -j DROP 2>/dev/null
}

for ipt in iptables iptables-legacy; do
    command -v "$ipt" >/dev/null 2>&1 || continue
    if healthy "$ipt"; then
        exit 0
    fi
done
exit 1
