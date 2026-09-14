#!/bin/sh
# Isolates the workload bridge (default 198.18.247.0/24, RFC 2544 space):
# public internet egress stays open while host services, private networks,
# link-local/metadata endpoints, and cluster ranges are unreachable from
# workload containers. Idempotent: rebuilds its own chains and re-links them.
#
# Two hooks are required because Docker only consults DOCKER-USER for
# *forwarded* traffic: connections terminating on the host itself (including
# the bridge gateway) traverse INPUT instead.
#
# Runs in the host network namespace (needs NET_ADMIN): as a compose service
# or Kubernetes DaemonSet (infra/k8s/base/workload-firewall.yaml — keep the
# rules in sync). Go code cannot do this through the filtered Docker socket
# proxy. Tools are baked into the firewall image; re-applied on every start.
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

# Forwarded path: replies first, then deny infrastructure, then DNS.
# DNS stays after the drops so only public resolvers remain reachable —
# infrastructure resolvers in the ranges below are unreachable.
iptables -A "$CHAIN" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
# Own subnet as destination covers the bridge gateway (.1) and peers (ICC off).
for cidr in "$SUBNET" 127.0.0.0/8 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16 224.0.0.0/4; do
	iptables -A "$CHAIN" -s "$SUBNET" -d "$cidr" -j DROP
done
# Non-RFC1918 cluster ranges (EKS/GKE pod/service CIDRs) come from the environment.
for cidr in $EXTRA_CIDRS; do
	iptables -A "$CHAIN" -s "$SUBNET" -d "$cidr" -j DROP
done
iptables -A "$CHAIN" -s "$SUBNET" -p udp --dport 53 -j ACCEPT
iptables -A "$CHAIN" -s "$SUBNET" -p tcp --dport 53 -j ACCEPT
# No new inbound connections to workload containers (they publish no ports).
iptables -A "$CHAIN" -d "$SUBNET" -m conntrack --ctstate NEW -j DROP
iptables -A "$CHAIN" -s "$SUBNET" -j ACCEPT

# Host-input path: the host initiates nothing toward workloads, so allow
# replies and drop every new connection workloads open against the host.
iptables -A "$CHAIN_IN" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A "$CHAIN_IN" -s "$SUBNET" -m conntrack --ctstate NEW -j DROP

# Workloads receive no IPv6 subnet; still drop infra-bound v6 explicitly.
if command -v ip6tables >/dev/null; then
	ip6tables -N "${CHAIN}6" 2>/dev/null || ip6tables -F "${CHAIN}6"
	ip6tables -C DOCKER-USER -j "${CHAIN}6" 2>/dev/null || ip6tables -I DOCKER-USER -j "${CHAIN}6"
	ip6tables -N "${CHAIN_IN}6" 2>/dev/null || ip6tables -F "${CHAIN_IN}6"
	ip6tables -C INPUT -j "${CHAIN_IN}6" 2>/dev/null || ip6tables -I INPUT -j "${CHAIN_IN}6"
	ip6tables -A "${CHAIN}6" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
	for cidr in ::1/128 fe80::/10 fc00::/7 ff00::/8; do
		ip6tables -A "${CHAIN}6" -d "$cidr" -j DROP
	done
	# No DNS carve-out: workloads hold no IPv6 addresses, so all v6 DNS
	# leaves over IPv4 (allowed above); v6 infra stays unreachable.
	ip6tables -A "${CHAIN}6" -j ACCEPT
	ip6tables -A "${CHAIN_IN}6" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
	# No workload v6 subnet exists; container-originated v6 (if any) is
	# link-local/ULA — drop new host-bound connections from those only.
	for cidr in fe80::/10 fc00::/7; do
		ip6tables -A "${CHAIN_IN}6" -s "$cidr" -m conntrack --ctstate NEW -j DROP
	done
fi

echo "workload-firewall: enforced for $SUBNET"
