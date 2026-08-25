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
