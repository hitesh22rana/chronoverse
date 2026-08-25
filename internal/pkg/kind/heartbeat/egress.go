package heartbeat

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"
)

// ErrDisallowedTarget is returned when a heartbeat endpoint resolves to a
// protected address space (loopback, private, link-local, multicast, or the
// unspecified address). Callers can test for it with errors.Is to map the
// condition onto a permanent user error instead of a retryable one.
var ErrDisallowedTarget = errors.New("heartbeat endpoint resolves to a disallowed address")

// maxDialTimeout bounds the per-attempt dial wait independently of the overall
// request timeout so dead internal addresses fail fast.
const maxDialTimeout = 10 * time.Second

// isDisallowedIP reports whether the IP falls into address ranges that must
// never be reached by user-defined heartbeat workflows.
func isDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if isReservedOrNonUnicast(ip) {
		return true
	}
	// IANA 192.0.0.0/24 is reserved but contains two globally reachable
	// anycast exceptions that must satisfy the public-endpoint policy:
	// 192.0.0.9 (PCP Anycast, RFC 7723) and 192.0.0.10 (TURN Anycast,
	// RFC 8155). Allow them before the denylist.
	if isGlobal192_0_0_24Exception(ip) {
		return false
	}
	// NAT64 well-known prefix 64:ff9b::/96 — decode embedded IPv4 and
	// apply the IPv4 guard. IANA marks this prefix globally reachable
	// (RFC 6052) for DNS64 synthesis, so blocking the prefix outright
	// would break IPv6-only workers reaching public IPv4-only endpoints.
	// Only /96 is specified for low-32-bit embedding; the local-use
	// 64:ff9b:1::/48 must remain blocked entirely per RFC 8215 (IPv4 bits
	// at 48-63 and 72-87, suffix ignored by translators).
	if isNAT64WellKnown(ip) {
		return isDisallowedIP(extractEmbeddedIPv4(ip, 96))
	}
	if isNAT64Local(ip) {
		return true
	}
	// RFC 6052 network-specific Pref64 prefixes configured by the operator:
	// decode the embedded IPv4 and apply the IPv4 guard, exactly like the
	// well-known prefix. Checked before the global-unicast and special-use
	// screens so test/documentary NSPs remain configurable.
	if bits, ok := longestMatchingNAT64Prefix(ip); ok {
		return isDisallowedIP(extractEmbeddedIPv4(ip, bits))
	}
	if !isGlobalUnicastStrict(ip) {
		return true
	}
	// 2001::/23 is IANA non-globally reachable except for more-specific
	// globally reachable allocations. Default-deny the /23 and allow only
	// the known global exceptions, rather than permitting everything not
	// individually listed.
	if isIn2001_23(ip) && !isGlobal2001_23Exception(ip) {
		return true
	}
	return isSpecialUse(ip)
}

// isReservedOrNonUnicast reports whether the address is unspecified,
// loopback, private, link-local, multicast, or interface-local multicast —
// ranges that must never be reached by user-defined workflows.
func isReservedOrNonUnicast(ip net.IP) bool {
	return ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast()
}

// longestMatchingNAT64Prefix reports the prefix length of the most-specific
// configured RFC 6052 prefix containing ip (prefixes may nest; the longest
// wins so decoding uses the narrowest embedding layout). ok is false when no
// configured prefix matches.
func longestMatchingNAT64Prefix(ip net.IP) (bits int, ok bool) {
	best := -1
	for i := range customNAT64Prefixes {
		if customNAT64Prefixes[i].net.Contains(ip) &&
			(best == -1 || customNAT64Prefixes[i].bits > customNAT64Prefixes[best].bits) {
			best = i
		}
	}
	if best == -1 {
		return 0, false
	}
	return customNAT64Prefixes[best].bits, true
}

func isGlobalUnicastStrict(ip net.IP) bool {
	if ip.To4() != nil {
		return true
	}
	if len(ip) != net.IPv6len {
		return false
	}
	// IPv6 global unicast is 2000::/3 (0010... to 0011...)
	return ip[0] >= 0x20 && ip[0] <= 0x3f
}

var (
	nat64WellKnownCIDR *net.IPNet
	nat64LocalCIDR     *net.IPNet
	specialCIDRs       []*net.IPNet
	rfc2001_23CIDR     *net.IPNet
	global2001_23CIDRs []*net.IPNet
)

func isNAT64WellKnown(ip net.IP) bool {
	return nat64WellKnownCIDR != nil && nat64WellKnownCIDR.Contains(ip)
}

func isNAT64Local(ip net.IP) bool {
	return nat64LocalCIDR != nil && nat64LocalCIDR.Contains(ip)
}

// nat64Prefix is one operator-configured RFC 6052 network-specific Pref64
// prefix. bits records the prefix length, which determines where the
// embedded IPv4 address sits inside the address.
type nat64Prefix struct {
	net  *net.IPNet
	bits int
}

// customNAT64Prefixes holds the deployment's Pref64 prefixes. It is written
// once by ConfigureNAT64Prefixes during startup (before any traffic) and only
// read afterwards; tests may reconfigure sequentially.
var customNAT64Prefixes []nat64Prefix

// ConfigureNAT64Prefixes registers the deployment's RFC 6052 network-specific
// Pref64 prefixes (for example an ISP- or enterprise-assigned /96). Addresses
// under a configured prefix are decoded to their embedded IPv4 address and
// re-checked by the same policy as direct IPv4 targets, so NAT64 translation
// cannot smuggle traffic to protected IPv4 space. Call this during startup,
// before any heartbeat executes.
//
// Each entry must be an IPv6 CIDR whose length is one of the RFC 6052
// embedding layouts: /32, /40, /48, /56, /64 or /96. Prefix lengths outside
// that set are rejected because the embedded IPv4 position would be ambiguous.
func ConfigureNAT64Prefixes(specs []string) error {
	parsed := make([]nat64Prefix, 0, len(specs))
	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		base, ipnet, err := net.ParseCIDR(spec)
		if err != nil {
			return fmt.Errorf("invalid NAT64 prefix %q: %w", spec, err)
		}
		bits, _ := ipnet.Mask.Size()
		if base.To4() != nil {
			return fmt.Errorf("invalid NAT64 prefix %q: must be an IPv6 CIDR", spec)
		}
		switch bits {
		case 32, 40, 48, 56, 64, 96:
		default:
			return fmt.Errorf("invalid NAT64 prefix %q: length must be one of the RFC 6052 embedding layouts /32, /40, /48, /56, /64 or /96", spec)
		}
		parsed = append(parsed, nat64Prefix{net: ipnet, bits: bits})
	}
	customNAT64Prefixes = parsed
	return nil
}

// extractEmbeddedIPv4 decodes the RFC 6052 IPv4 representation embedded at
// the given prefix length. Layouts shorter than /64 skip the reserved u-octet
// at byte 8 of the address (RFC 6052 section 2.2).
func extractEmbeddedIPv4(ip net.IP, bits int) net.IP {
	b := ip.To16()
	switch bits {
	case 96:
		return net.IPv4(b[12], b[13], b[14], b[15])
	case 64:
		return net.IPv4(b[8], b[9], b[10], b[11])
	case 32:
		return net.IPv4(b[4], b[5], b[6], b[7])
	case 40:
		return net.IPv4(b[5], b[6], b[7], b[9])
	case 48:
		return net.IPv4(b[6], b[7], b[9], b[10])
	case 56:
		return net.IPv4(b[7], b[9], b[10], b[11])
	default:
		return nil
	}
}

func isIn2001_23(ip net.IP) bool {
	return rfc2001_23CIDR != nil && rfc2001_23CIDR.Contains(ip)
}

func isGlobal2001_23Exception(ip net.IP) bool {
	for _, cidr := range global2001_23CIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	// Three anycast addresses 2001:1::1, 2001:1::2, 2001:1::3
	if ip.Equal(net.ParseIP("2001:1::1")) || ip.Equal(net.ParseIP("2001:1::2")) || ip.Equal(net.ParseIP("2001:1::3")) {
		return true
	}
	return false
}

func isGlobal192_0_0_24Exception(ip net.IP) bool {
	return ip.Equal(net.ParseIP("192.0.0.9")) || ip.Equal(net.ParseIP("192.0.0.10"))
}

func init() {
	var err error
	_, nat64WellKnownCIDR, err = net.ParseCIDR("64:ff9b::/96")
	if err != nil {
		panic(err)
	}
	_, nat64LocalCIDR, err = net.ParseCIDR("64:ff9b:1::/48")
	if err != nil {
		panic(err)
	}
	_, rfc2001_23CIDR, err = net.ParseCIDR("2001::/23")
	if err != nil {
		panic(err)
	}
	for _, cidr := range []string{
		"2001:3::/32",
		"2001:4:112::/48",
		"2001:20::/28",
		"2001:30::/28",
	} {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		global2001_23CIDRs = append(global2001_23CIDRs, n)
	}
	for _, cidr := range []string{
		// RFC 6598 CGNAT
		"100.64.0.0/10",
		// IANA IPv4 special-use
		"0.0.0.0/8",
		"192.0.0.0/24",
		"192.0.2.0/24",
		"192.88.99.0/24",
		"198.18.0.0/15",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"240.0.0.0/4",
		// IPv6 special-use — within 2000::/3 but still non-public
		"2001::/32",
		"2001:10::/28",
		"2001:100::/24",
		"2001:2::/48",
		"2001:5::/32",
		"2001:db8::/32",
		"2002::/16",
		"3fff::/20",
		"5f00::/8",
	} {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		specialCIDRs = append(specialCIDRs, n)
	}
}

func isSpecialUse(ip net.IP) bool {
	for _, cidr := range specialCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// dialTimeoutFor clamps the per-attempt dial wait so dead internal addresses
// fail fast regardless of the overall request timeout.
func dialTimeoutFor(requestTimeout time.Duration) time.Duration {
	if requestTimeout <= 0 || requestTimeout > maxDialTimeout {
		return maxDialTimeout
	}
	return requestTimeout
}

// newGuardedDialer returns a dialer that validates the resolved destination
// address inside its Control hook, i.e. at connection-establishment time.
//
// Checking here (instead of once before issuing the request) closes two bypass
// classes: multi-homed/redirected responses that hop to another target, and
// DNS rebinding where a name resolves public before the check and private at
// dial time. Every connection attempt of every redirect hop is re-validated.
func newGuardedDialer(requestTimeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout: dialTimeoutFor(requestTimeout),
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("invalid dial address %q: %w", address, err)
			}

			ip := net.ParseIP(host)
			if isDisallowedIP(ip) {
				return fmt.Errorf("%w: %s", ErrDisallowedTarget, host)
			}

			return nil
		},
	}
}
