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
command -v iptables >/dev/null || { echo "workload-firewall: iptables not available" >&2; exit 1; }

# Add-if-missing: never flush, so a re-apply cannot open a window.
rule() { iptables -C "$CHAIN" "$@" 2>/dev/null || iptables -A "$CHAIN" "$@"; }
rule_in() { iptables -C "$CHAIN_IN" "$@" 2>/dev/null || iptables -A "$CHAIN_IN" "$@"; }
rule6() { ip6tables -C "${CHAIN}6" "$@" 2>/dev/null || ip6tables -A "${CHAIN}6" "$@"; }
rule6_in() { ip6tables -C "${CHAIN_IN}6" "$@" 2>/dev/null || ip6tables -A "${CHAIN_IN}6" "$@"; }
ensure_chain() { iptables -n -L "$1" >/dev/null 2>&1 || iptables -N "$1"; }
ensure_chain6() { ip6tables -n -L "$1" >/dev/null 2>&1 || ip6tables -N "$1"; }

ensure_chain "$CHAIN"
ensure_chain "$CHAIN_IN"

# Deny-before-allow: drop the terminal accept first so added ranges can't land
# after it, then re-append it last (removal only ever fails closed).
iptables -D "$CHAIN" -s "$SUBNET" -i "$BRIDGE_IF" -j ACCEPT 2>/dev/null || true

# Forwarded path: replies, infra drops, then DNS.
# ESTABLISHED stays interface-free: return traffic ingresses externally, and
# it accepts replies only, never new flows.
rule -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
# Own subnet as dst covers the gateway (.1) and peers.
for cidr in "$SUBNET" 127.0.0.0/8 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16 224.0.0.0/4; do
    rule -i "$BRIDGE_IF" -s "$SUBNET" -d "$cidr" -j DROP
done
# Extra cluster ranges come from the environment.
for cidr in $EXTRA_CIDRS; do
    rule -i "$BRIDGE_IF" -s "$SUBNET" -d "$cidr" -j DROP
done
# DNS after drops: infra resolvers unreachable, public DNS works.
rule -i "$BRIDGE_IF" -s "$SUBNET" -p udp --dport 53 -j ACCEPT
rule -i "$BRIDGE_IF" -s "$SUBNET" -p tcp --dport 53 -j ACCEPT
# No new inbound (no published ports); presence proves a complete apply.
rule -d "$SUBNET" -m conntrack --ctstate NEW -j DROP
rule -s "$SUBNET" -i "$BRIDGE_IF" -j ACCEPT

# Host-input path: the host initiates nothing here; allow replies, drop new.
# Source-subnet match is already precise here (bridge-ingress or spoofed —
# both dropped), so no interface match is needed.
rule_in -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
rule_in -s "$SUBNET" -m conntrack --ctstate NEW -j DROP

# IPv6: link-local sources are shared, hence -i; no ACCEPT (RETURN to Docker).
if command -v ip6tables >/dev/null; then
    ensure_chain6 "${CHAIN}6"
    ensure_chain6 "${CHAIN_IN}6"
    rule6 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    for cidr in ::1/128 fe80::/10 fc00::/7 ff00::/8; do
        rule6 -i "$BRIDGE_IF" -s fe80::/10 -d "$cidr" -j DROP
    done
    # No v6 DNS carve-out: it leaves over IPv4.
    rule6_in -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    for cidr in fe80::/10 fc00::/7; do
        rule6_in -i "$BRIDGE_IF" -s "$cidr" -m conntrack --ctstate NEW -j DROP
    done
    ip6tables -C INPUT -j "${CHAIN_IN}6" 2>/dev/null || ip6tables -I INPUT -j "${CHAIN_IN}6"
    ip6tables -C DOCKER-USER -j "${CHAIN}6" 2>/dev/null || ip6tables -I DOCKER-USER -j "${CHAIN}6"
fi

# Link jumps last: no empty-chain window.
iptables -C INPUT -j "$CHAIN_IN" 2>/dev/null || iptables -I INPUT -j "$CHAIN_IN"
iptables -C DOCKER-USER -j "$CHAIN" 2>/dev/null || iptables -I DOCKER-USER -j "$CHAIN"

if [ -n "$READY_FILE" ]; then
    touch "$READY_FILE"
fi
echo "workload-firewall: enforced for $SUBNET"
