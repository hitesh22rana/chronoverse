//nolint:testpackage // Checks malformed IP encodings at the pure policy boundary.
package heartbeat

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIPPolicyRejectsMalformedEncodings(t *testing.T) {
	for _, ip := range []net.IP{nil, {}, {1, 2, 3}} {
		require.False(t, isGlobalUnicastStrict(ip))
		require.Nil(t, extractEmbeddedIPv4(ip, 96))
	}
	require.Nil(t, extractEmbeddedIPv4(net.ParseIP("64:ff9b::192.0.2.1"), 80))
	require.Equal(t, net.ParseIP("192.0.2.1").To4(), extractEmbeddedIPv4(net.ParseIP("64:ff9b::192.0.2.1"), 96).To4())
}

func TestCompiledNetworkParsingPreservesMaskAndPanic(t *testing.T) {
	for _, raw := range []string{"192.0.2.0/24", "192.0.2.129/24", "64:ff9b::/96"} {
		_, expected, err := net.ParseCIDR(raw)
		require.NoError(t, err)
		require.Equal(t, expected.String(), mustCIDR(raw).String())
	}
	require.Panics(t, func() { mustCIDR("invalid") })
}
