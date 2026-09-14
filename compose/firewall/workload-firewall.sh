#!/bin/sh
# Applies host firewall rules isolating the workload bridge (default
# 198.18.247.0/24, RFC 2544 space): public internet egress stays open while
# host, private, link-local/metadata, CGNAT, and cluster destinations are
# dropped. Idempotent: rebuilds its own chain and re-links it into DOCKER-USER.
#
# Runs as a compose one-shot in the host network namespace (needs NET_ADMIN);
# Go code cannot do this through the filtered Docker socket proxy. Re-applied
# on every (re)start, so host reboots are covered.
set -eu

SUBNET="${WORKLOAD_SUBNET:?set WORKLOAD_SUBNET (must match EXECUTION_WORKER_WORKLOAD_SUBNET)}"
EXTRA_CIDRS="${CLUSTER_CIDRS:-}"
CHAIN="CHRONOVERSE-WORKLOAD"

apk add --no-cache iptables ip6tables >/dev/null 2>&1 || true
command -v iptables >/dev/null || { echo "workload-firewall: iptables not available" >&2; exit 1; }

iptables -N "$CHAIN" 2>/dev/null || iptables -F "$CHAIN"
iptables -C DOCKER-USER -j "$CHAIN" 2>/dev/null || iptables -I DOCKER-USER -j "$CHAIN"

# Replies first, then DNS (gateway-provided resolver), then deny infrastructure.
iptables -A "$CHAIN" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A "$CHAIN" -s "$SUBNET" -p udp --dport 53 -j ACCEPT
iptables -A "$CHAIN" -s "$SUBNET" -p tcp --dport 53 -j ACCEPT
# Own subnet as destination covers the bridge gateway (.1) and peers (ICC off).
for cidr in "$SUBNET" 127.0.0.0/8 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16 224.0.0.0/4; do
	iptables -A "$CHAIN" -s "$SUBNET" -d "$cidr" -j DROP
done
# Non-RFC1918 cluster ranges (EKS/GKE pod/service CIDRs) come from the environment.
for cidr in $EXTRA_CIDRS; do
	iptables -A "$CHAIN" -s "$SUBNET" -d "$cidr" -j DROP
done
# No new inbound connections to workload containers (they publish no ports).
iptables -A "$CHAIN" -d "$SUBNET" -m conntrack --ctstate NEW -j DROP
iptables -A "$CHAIN" -s "$SUBNET" -j ACCEPT

# Workloads receive no IPv6 subnet; still drop infra-bound v6 explicitly.
if command -v ip6tables >/dev/null; then
	ip6tables -N "${CHAIN}6" 2>/dev/null || ip6tables -F "${CHAIN}6"
	ip6tables -C DOCKER-USER -j "${CHAIN}6" 2>/dev/null || ip6tables -I DOCKER-USER -j "${CHAIN}6"
	ip6tables -A "${CHAIN}6" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
	for cidr in ::1/128 fe80::/10 fc00::/7 ff00::/8; do
		ip6tables -A "${CHAIN}6" -d "$cidr" -j DROP
	done
	ip6tables -A "${CHAIN}6" -j ACCEPT
fi

echo "workload-firewall: enforced for $SUBNET"
