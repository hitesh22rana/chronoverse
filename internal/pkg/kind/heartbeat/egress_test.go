//nolint:testpackage // Unit tests exercise unexported guard internals (isDisallowedIP, newGuardedDialer).
package heartbeat

import (
	"errors"
	"net"
	"testing"
)

func TestIsDisallowedIP(t *testing.T) {
	tests := []struct {
		ip         string
		disallowed bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.5", true},
		{"172.16.0.9", true},
		{"172.31.255.255", true},
		{"192.168.1.10", true},
		{"169.254.169.254", true},
		{"fe80::1", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"ff02::1", true},

		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
		{"2606:4700:4700::1111", false},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if got := isDisallowedIP(ip); got != tt.disallowed {
			t.Errorf("isDisallowedIP(%s) = %v, want %v", tt.ip, got, tt.disallowed)
		}
	}
}

func TestIsDisallowedIP_CGNATAndSpecialUse(t *testing.T) {
	tests := []struct {
		ip         string
		disallowed bool
	}{
		// RFC 6598 CGNAT
		{"100.64.0.1", true},
		{"100.100.100.200", true},
		{"100.127.255.255", true},
		{"100.128.0.1", false},
		// IANA IPv4 special-use
		{"0.0.0.1", true},
		{"192.0.0.1", true},
		{"192.0.0.9", false},
		{"192.0.0.10", false},
		{"192.0.2.1", true},
		{"192.88.99.2", true},
		{"198.18.0.1", true},
		{"198.51.100.1", true},
		{"203.0.113.1", true},
		{"240.0.0.1", true},
		// IPv6 special-use — NAT64 local must remain blocked
		{"64:ff9b:1::1", true},
		{"64:ff9b:1:a00:0:100:808:808", true},
		// IPv6 non-global outside 2000::/3
		{"100:0:0:1::1", true},
		{"fec0::1", true},
		{"5f00::1", true},
		// 2001::/23 default-deny
		{"2001:5::1", true},
		{"2001:6::1", true},
		{"2001:7::1", true},
		{"2001:100::1", true},
		{"2001:2::1", true},
		// 2001::/23 globally reachable exceptions must remain allowed
		{"2001:3::1", false},
		{"2001:4:112::1", false},
		{"2001:20::1", false},
		{"2001:30::1", false},
		{"2001:1::1", false},
		{"2001:1::2", false},
		{"2001:1::3", false},
		// NAT64 well-known with embedded IPv4 guard
		{"64:ff9b::8.8.8.8", false},
		{"64:ff9b::10.0.0.1", true},
		{"64:ff9b::192.0.2.1", true},
		// Public globals remain allowed
		{"2001:4860:4860::8888", false},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if got := isDisallowedIP(ip); got != tt.disallowed {
			t.Errorf("isDisallowedIP(%s) = %v, want %v", tt.ip, got, tt.disallowed)
		}
	}
}

func TestGuardedDialerControl(t *testing.T) {
	control := newGuardedDialer(0).Control

	tests := []struct {
		address    string
		wantErr    bool
		isSentinel bool
	}{
		{"127.0.0.1:8080", true, true},
		{"[::1]:3000", true, true},
		{"10.1.2.3:443", true, true},
		{"169.254.169.254:80", true, true},
		{"192.168.0.20:6379", true, true},
		{"100.64.0.1:80", true, true},
		{"192.0.0.1:80", true, true},
		{"192.0.0.9:80", false, false},
		{"192.0.0.10:80", false, false},
		{"[64:ff9b:1::1]:80", true, true},
		{"[64:ff9b::8.8.8.8]:80", false, false},
		{"[64:ff9b::10.0.0.1]:80", true, true},
		{"[2001:5::1]:443", true, true},
		{"[2001:3::1]:443", false, false},
		{"[2001:1::1]:443", false, false},
		{"93.184.216.34:80", false, false},
		{"[2606:4700:4700::1111]:443", false, false},
		{"not-an-address", true, false}, // malformed address errors without sentinel
	}

	for _, tt := range tests {
		err := control("tcp", tt.address, nil)
		if (err != nil) != tt.wantErr {
			t.Errorf("control(%q) error = %v, wantErr %v", tt.address, err, tt.wantErr)
			continue
		}
		if tt.isSentinel && !errors.Is(err, ErrDisallowedTarget) {
			t.Errorf("control(%q) error = %v, want ErrDisallowedTarget", tt.address, err)
		}
	}
}

func TestNAT64NetworkSpecificPrefixes(t *testing.T) {
	// Independent RFC 6052 section 2.2 embedding offsets: byte positions of
	// the four IPv4 octets inside a 16-byte address. Prefixes shorter than
	// /64 skip the reserved u-octet at byte 8.
	offsets := map[int][4]int{
		32: {4, 5, 6, 7},
		40: {5, 6, 7, 9},
		48: {6, 7, 9, 10},
		56: {7, 9, 10, 11},
		64: {8, 9, 10, 11},
		96: {12, 13, 14, 15},
	}
	prefixFor := map[int]string{
		32: "2606:4700::/32",
		40: "2620:0:aa00::/40",
		48: "2800:1f0:1234::/48",
		56: "2402:9400:4321:4300::/56",
		64: "2607:f8b0:5555:5555::/64",
		96: "2605:1234:5678:9abc:def0::/96",
	}

	build := func(t *testing.T, prefix string, bits int, v4 [4]byte) net.IP {
		t.Helper()
		base, _, err := net.ParseCIDR(prefix)
		if err != nil {
			t.Fatalf("parse prefix %q: %v", prefix, err)
		}
		addr := make(net.IP, net.IPv6len)
		copy(addr, base.To16())
		for i, o := range offsets[bits] {
			addr[o] = v4[i]
		}
		return addr
	}

	// Before configuration, an address under a globally routed network-specific
	// Pref64 /96 embedding a private IPv4 passes as ordinary global unicast.
	unconfigured := build(t, prefixFor[96], 96, [4]byte{10, 0, 0, 1})
	if !isGlobalUnicastStrict(unconfigured) || isDisallowedIP(unconfigured) {
		t.Fatalf("expected %s to pass as global unicast before configuration", unconfigured)
	}

	specs := make([]string, 0, len(prefixFor))
	for bits := range prefixFor {
		specs = append(specs, prefixFor[bits])
	}
	if err := ConfigureNAT64Prefixes(specs); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() {
		if err := ConfigureNAT64Prefixes(nil); err != nil {
			t.Errorf("reset prefixes: %v", err)
		}
	})

	for bits := range prefixFor {
		private := build(t, prefixFor[bits], bits, [4]byte{10, 0, 0, 1})
		public := build(t, prefixFor[bits], bits, [4]byte{93, 184, 216, 34})
		if !isDisallowedIP(private) {
			t.Errorf("/%d: embedded private IPv4 not rejected: %s", bits, private)
		}
		if isDisallowedIP(public) {
			t.Errorf("/%d: embedded public IPv4 wrongly rejected: %s", bits, public)
		}
	}
}

func TestConfigureNAT64PrefixesRejectsInvalidSpecs(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"ipv4 cidr", "10.0.0.0/96"},
		{"unsupported length", "2606:4700::/24"},
		{"garbage", "not-a-cidr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ConfigureNAT64Prefixes([]string{tt.spec})
			if err == nil {
				t.Fatalf("expected error for %q", tt.spec)
			}
		})
	}

	// Blank entries are tolerated so comma-joined environment values can carry
	// stray whitespace without failing startup.
	if err := ConfigureNAT64Prefixes([]string{"", "  ", "2606:4700::/96"}); err != nil {
		t.Fatalf("expected blank-tolerant parse, got %v", err)
	}
	t.Cleanup(func() {
		if err := ConfigureNAT64Prefixes(nil); err != nil {
			t.Errorf("reset prefixes: %v", err)
		}
	})
}
