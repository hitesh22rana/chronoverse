#!/bin/sh
# Isolates the workload bridge (default 198.18.247.0/24): internet open,
# host/private/metadata/cluster unreachable. Idempotent: rules are checked
# before adding, never flushed — no unprotected window on re-apply.
# Mirrored into the workload-firewall-script ConfigMap — keep in sync.
#
# Two hooks: DOCKER-USER sees only *forwarded* traffic; host-terminating
# connections (incl. the gateway) traverse INPUT. Jumps link last.
#
# Workload-source rules match the bridge interface (-i): v6 sources are shared
# link-local space, and -i survives subnet reuse. The name is fixed at creation
# (resolving it here would deadlock fresh installs). Interface-free rules are
# verdict-identical either way, so probes stay host-agnostic.
#
# Backend: Docker uses nftables or xtables-legacy (invisible to each other).
# Detect the view holding DOCKER-USER once; probes check the same way.
#
# Host netns + NET_ADMIN (socket proxy can't program rules); plain alpine +
# runtime install, so registry access is required on (re)create.
set -eu

SUBNET="${WORKLOAD_SUBNET:?set WORKLOAD_SUBNET (must match EXECUTION_WORKER_WORKLOAD_SUBNET)}"
BRIDGE_IF="${WORKLOAD_BRIDGE_IF:-chronoverse-br}"
EXTRA_CIDRS="${CLUSTER_CIDRS:-}"
READY_FILE="${READY_FILE:-}"
CHAIN="CHRONOVERSE-WORKLOAD"
CHAIN_IN="CHRONOVERSE-WORKLOAD-IN"

apk add --no-cache iptables ip6tables
apk add --no-cache iptables-legacy 2>/dev/null || true

# Prints the binary seeing DOCKER-USER, or nothing.
pick_backend() {
    if "$1" -n -L DOCKER-USER >/dev/null 2>&1; then
        echo "$1"
    elif command -v "$2" >/dev/null 2>&1 && "$2" -n -L DOCKER-USER >/dev/null 2>&1; then
        echo "$2"
    fi
}

IPT="$(pick_backend iptables iptables-legacy)"
[ -n "$IPT" ] || { echo "workload-firewall: no DOCKER-USER chain in any backend" >&2; exit 1; }
# No ip6tables-legacy exists: v6 is nft-only, skipped when invisible.
IP6T=""
if command -v ip6tables >/dev/null 2>&1 && ip6tables -n -L DOCKER-USER >/dev/null 2>&1; then
    IP6T="ip6tables"
fi

# Add-if-missing: never flush, so a re-apply cannot open a window.
rule() { b="$1"; shift; "$b" -C "$CHAIN" "$@" 2>/dev/null || "$b" -A "$CHAIN" "$@"; }
rule_in() { b="$1"; shift; "$b" -C "$CHAIN_IN" "$@" 2>/dev/null || "$b" -A "$CHAIN_IN" "$@"; }
# rule_in_at inserts (positionally) instead of appending, for allows that must
# stay ahead of the terminal DROP without ever deleting it.
rule_in_at() { b="$1"; pos="$2"; shift 2; "$b" -C "$CHAIN_IN" "$@" 2>/dev/null || "$b" -I "$CHAIN_IN" "$pos" "$@"; }
rule6() { "$IP6T" -C "${CHAIN}6" "$@" 2>/dev/null || "$IP6T" -A "${CHAIN}6" "$@"; }
rule6_in() { "$IP6T" -C "${CHAIN_IN}6" "$@" 2>/dev/null || "$IP6T" -A "${CHAIN_IN}6" "$@"; }
ensure_chain() { "$IPT" -n -L "$1" >/dev/null 2>&1 || "$IPT" -N "$1"; }
ensure_chain6() { "$IP6T" -n -L "$1" >/dev/null 2>&1 || "$IP6T" -N "$1"; }

ensure_chain "$CHAIN"
ensure_chain "$CHAIN_IN"

# Deny-before-allow: drop terminal + DNS accepts first so added ranges can't
# land after an allow, then re-add in order (removal only fails closed).
"$IPT" -D "$CHAIN" -s "$SUBNET" -i "$BRIDGE_IF" -j ACCEPT 2>/dev/null || true
"$IPT" -D "$CHAIN" -s "$SUBNET" -i "$BRIDGE_IF" -p udp --dport 53 -j ACCEPT 2>/dev/null || true
"$IPT" -D "$CHAIN" -s "$SUBNET" -i "$BRIDGE_IF" -p tcp --dport 53 -j ACCEPT 2>/dev/null || true

# Forwarded path: replies, infra drops, then DNS.
# ESTABLISHED is interface-scoped: our chains precede operator rules, so an
# unscoped accept would shield unrelated established flows from them.
rule "$IPT" -i "$BRIDGE_IF" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
# Own subnet as dst covers the gateway (.1) and peers.
for cidr in "$SUBNET" 127.0.0.0/8 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16 224.0.0.0/4; do
    rule "$IPT" -i "$BRIDGE_IF" -s "$SUBNET" -d "$cidr" -j DROP
done
# Extra cluster ranges come from the environment.
for cidr in $EXTRA_CIDRS; do
    rule "$IPT" -i "$BRIDGE_IF" -s "$SUBNET" -d "$cidr" -j DROP
done
# DNS after drops: infra resolvers unreachable, public DNS works.
rule "$IPT" -i "$BRIDGE_IF" -s "$SUBNET" -p udp --dport 53 -j ACCEPT
rule "$IPT" -i "$BRIDGE_IF" -s "$SUBNET" -p tcp --dport 53 -j ACCEPT
# No new inbound (no published ports); last rule proves a complete apply.
rule "$IPT" -d "$SUBNET" -m conntrack --ctstate NEW -j DROP
rule "$IPT" -s "$SUBNET" -i "$BRIDGE_IF" -j ACCEPT

# Host-input path: the host initiates nothing here; allow replies, drop new.
# Plus the Docker-provided resolver: on Linux hosts 127.0.0.11 is served from
# the bridge gateway, so workloads need host port 53 (nothing else) to resolve.
# Unlike the forwarded path, the terminal DROP is never deleted here: DNS goes
# in positionally ahead of it, so re-apply has no fail-open window at all.
rule_in "$IPT" -i "$BRIDGE_IF" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
rule_in_at "$IPT" 2 -i "$BRIDGE_IF" -s "$SUBNET" -p udp --dport 53 -j ACCEPT
rule_in_at "$IPT" 2 -i "$BRIDGE_IF" -s "$SUBNET" -p tcp --dport 53 -j ACCEPT
rule_in "$IPT" -s "$SUBNET" -m conntrack --ctstate NEW -j DROP

# IPv6: link-local sources are shared, hence -i; no ACCEPT (RETURN to Docker).
# Skipped on IPv4-only hosts: no DOCKER-USER chain means no v6 forwarding to
# protect; linking into it would abort the whole apply under set -eu.
if [ -n "$IP6T" ]; then
    ensure_chain6 "${CHAIN}6"
    ensure_chain6 "${CHAIN_IN}6"
    rule6 -i "$BRIDGE_IF" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    for cidr in ::1/128 fe80::/10 fc00::/7 ff00::/8; do
        rule6 -i "$BRIDGE_IF" -s fe80::/10 -d "$cidr" -j DROP
    done
    # No v6 DNS carve-out: it leaves over IPv4.
    rule6_in -i "$BRIDGE_IF" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    for cidr in fe80::/10 fc00::/7; do
        rule6_in -i "$BRIDGE_IF" -s "$cidr" -m conntrack --ctstate NEW -j DROP
    done
    "$IP6T" -C INPUT -j "${CHAIN_IN}6" 2>/dev/null || "$IP6T" -I INPUT -j "${CHAIN_IN}6"
    "$IP6T" -C DOCKER-USER -j "${CHAIN}6" 2>/dev/null || "$IP6T" -I DOCKER-USER -j "${CHAIN}6"
fi

# Link jumps last: no empty-chain window.
"$IPT" -C INPUT -j "$CHAIN_IN" 2>/dev/null || "$IPT" -I INPUT -j "$CHAIN_IN"
"$IPT" -C DOCKER-USER -j "$CHAIN" 2>/dev/null || "$IPT" -I DOCKER-USER -j "$CHAIN"

if [ -n "$READY_FILE" ]; then
    touch "$READY_FILE"
fi
echo "workload-firewall: enforced for $SUBNET"
