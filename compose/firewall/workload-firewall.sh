#!/bin/sh
# Isolates the workload bridge (default 198.18.247.0/24): internet egress open,
# host/private/metadata/cluster unreachable. Idempotent: rebuilds its chains.
#
# Two hooks: DOCKER-USER sees only *forwarded* traffic; host-terminating
# connections (incl. the bridge gateway) traverse INPUT instead.
#
# Host netns + NET_ADMIN, as a compose service or DaemonSet
# (infra/k8s/base/workload-firewall.yaml — keep in sync); the socket proxy
# cannot program rules, and tools are baked into the firewall image.
set -eu

SUBNET="${WORKLOAD_SUBNET:?set WORKLOAD_SUBNET (must match EXECUTION_WORKER_WORKLOAD_SUBNET)}"
EXTRA_CIDRS="${CLUSTER_CIDRS:-}"
CHAIN="CHRONOVERSE-WORKLOAD"
CHAIN_IN="CHRONOVERSE-WORKLOAD-IN"

command -v iptables >/dev/null || { echo "workload-firewall: iptables not available" >&2; exit 1; }

iptables -N "$CHAIN" 2>/dev/null || iptables -F "$CHAIN"
iptables -C DOCKER-USER -j "$CHAIN" 2>/dev/null || iptables -I DOCKER-USER -j "$CHAIN"
iptables -N "$CHAIN_IN" 2>/dev/null || iptables -F "$CHAIN_IN"
iptables -C INPUT -j "$CHAIN_IN" 2>/dev/null || iptables -I INPUT -j "$CHAIN_IN"

# Forwarded path: replies, infra drops, then DNS.
# DNS after drops: infra resolvers unreachable, public DNS works.
iptables -A "$CHAIN" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
# Own subnet as dst covers the gateway (.1) and peers.
for cidr in "$SUBNET" 127.0.0.0/8 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16 224.0.0.0/4; do
	iptables -A "$CHAIN" -s "$SUBNET" -d "$cidr" -j DROP
done
# Extra non-RFC1918 cluster ranges come from the environment.
for cidr in $EXTRA_CIDRS; do
	iptables -A "$CHAIN" -s "$SUBNET" -d "$cidr" -j DROP
done
iptables -A "$CHAIN" -s "$SUBNET" -p udp --dport 53 -j ACCEPT
iptables -A "$CHAIN" -s "$SUBNET" -p tcp --dport 53 -j ACCEPT
# No new inbound: workloads publish no ports.
iptables -A "$CHAIN" -d "$SUBNET" -m conntrack --ctstate NEW -j DROP
iptables -A "$CHAIN" -s "$SUBNET" -j ACCEPT

# Host-input path: the host initiates nothing here; allow replies, drop new.
iptables -A "$CHAIN_IN" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A "$CHAIN_IN" -s "$SUBNET" -m conntrack --ctstate NEW -j DROP

# No workload v6 subnet; still drop infra-bound v6.
if command -v ip6tables >/dev/null; then
	ip6tables -N "${CHAIN}6" 2>/dev/null || ip6tables -F "${CHAIN}6"
	ip6tables -C DOCKER-USER -j "${CHAIN}6" 2>/dev/null || ip6tables -I DOCKER-USER -j "${CHAIN}6"
	ip6tables -N "${CHAIN_IN}6" 2>/dev/null || ip6tables -F "${CHAIN_IN}6"
	ip6tables -C INPUT -j "${CHAIN_IN}6" 2>/dev/null || ip6tables -I INPUT -j "${CHAIN_IN}6"
	ip6tables -A "${CHAIN}6" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
	for cidr in ::1/128 fe80::/10 fc00::/7 ff00::/8; do
		ip6tables -A "${CHAIN}6" -d "$cidr" -j DROP
	done
	# No v6 DNS carve-out: v6 DNS leaves over IPv4; v6 infra stays unreachable.
	ip6tables -A "${CHAIN}6" -j ACCEPT
	ip6tables -A "${CHAIN_IN}6" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
	# v6 from containers can only be link-local/ULA — drop it host-bound.
	for cidr in fe80::/10 fc00::/7; do
		ip6tables -A "${CHAIN_IN}6" -s "$cidr" -m conntrack --ctstate NEW -j DROP
	done
fi

echo "workload-firewall: enforced for $SUBNET"
