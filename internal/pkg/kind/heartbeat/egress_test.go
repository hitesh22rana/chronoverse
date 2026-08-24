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
