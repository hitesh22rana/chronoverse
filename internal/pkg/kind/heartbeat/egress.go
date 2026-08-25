package heartbeat

import (
	"errors"
	"fmt"
	"net"
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
	return ip == nil ||
		ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		!isGlobalUnicastStrict(ip) ||
		isSpecialUse(ip)
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

var specialCIDRs []*net.IPNet

func init() {
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
		// IPv6 special-use — IANA registries
		"64:ff9b::/96",
		"64:ff9b:1::/48",
		"100::/64",
		"100::/8",
		"2001::/23",
		"2001::/32",
		"2001:10::/28",
		"2001:2::/48",
		"2001:db8::/32",
		"2002::/16",
		"3fff::/20",
		"5f00::/8",
		"fec0::/10",
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

// newGuardedDialer returns a dialer that validates the resolved destination
// address inside its Control hook, i.e. at connection-establishment time.
//
// Checking here (instead of once before issuing the request) closes two bypass
// classes: multi-homed/redirected responses that hop to another target, and
// DNS rebinding where a name resolves public before the check and private at
// dial time. Every connection attempt of every redirect hop is re-validated.
func newGuardedDialer(requestTimeout time.Duration) *net.Dialer {
	dialTimeout := requestTimeout
	if dialTimeout <= 0 || dialTimeout > maxDialTimeout {
		dialTimeout = maxDialTimeout
	}

	return &net.Dialer{
		Timeout: dialTimeout,
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
