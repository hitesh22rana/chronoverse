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
		ip.IsMulticast()
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
