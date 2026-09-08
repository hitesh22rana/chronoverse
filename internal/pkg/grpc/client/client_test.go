package client_test

import (
	"net/url"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"

	grpcclient "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/client"
)

func TestNewClient_NormalizesDialTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want string
	}{
		{"k8s fully-qualified", "dns:///users-service.chronoverse.svc", "dns-poll:///users-service.chronoverse.svc:50051"},
		{"short dns scheme", "dns://users-service", "dns-poll:///users-service:50051"},
		{"bare compose hostname", "users-service", "dns-poll:///users-service:50051"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn, err := grpcclient.NewClient(
				&grpcclient.ServiceConfig{Host: tt.host, Port: 50051, TLS: &grpcclient.TLSConfig{}},
				nil,
				nil,
			)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			defer conn.Close()

			if got := conn.Target(); got != tt.want {
				t.Errorf("conn.Target() = %q, want %q", got, tt.want)
			}
			if resolver.Get("dns-poll") == nil {
				t.Error(`resolver.Get("dns-poll") = nil, want registered builder`)
			}
		})
	}
}

// stubClientConn records resolver updates without opening connections.
type stubClientConn struct {
	mu     sync.Mutex
	states []resolver.State
}

func (s *stubClientConn) UpdateState(st resolver.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = append(s.states, st)
	return nil
}

func (s *stubClientConn) ReportError(error) {}

func (s *stubClientConn) NewAddress([]resolver.Address) {}

func (s *stubClientConn) ParseServiceConfig(string) *serviceconfig.ParseResult { return nil }

func (s *stubClientConn) addressCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, st := range s.states {
		n += len(st.Addresses)
	}
	return n
}

// TestDNSPollBuilder_ResolvesAddresses guards the dial-target shape: the host
// must land in the URL path slot, otherwise gRPC resolves an empty endpoint
// and the channel ends up with zero addresses (every RPC fails fast).
func TestDNSPollBuilder_ResolvesAddresses(t *testing.T) {
	t.Parallel()

	conn, err := grpcclient.NewClient(
		&grpcclient.ServiceConfig{Host: "localhost", Port: 50051, TLS: &grpcclient.TLSConfig{}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer conn.Close()

	b := resolver.Get("dns-poll")
	if b == nil {
		t.Fatal(`resolver.Get("dns-poll") = nil, want registered builder`)
	}

	targetURL, err := url.Parse("dns-poll:///localhost:50051")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	cc := &stubClientConn{}
	r, err := b.Build(resolver.Target{URL: *targetURL}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer r.Close()

	deadline := time.Now().Add(10 * time.Second)
	for cc.addressCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for resolver addresses, want at least one for localhost")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDNSPollBuilder_AuthoritySlotYieldsNoAddresses(t *testing.T) {
	t.Parallel()

	conn, err := grpcclient.NewClient(
		&grpcclient.ServiceConfig{Host: "localhost", Port: 50051, TLS: &grpcclient.TLSConfig{}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer conn.Close()

	b := resolver.Get("dns-poll")
	if b == nil {
		t.Fatal(`resolver.Get("dns-poll") = nil, want registered builder`)
	}

	// Host in the authority slot leaves the endpoint empty, which can never
	// resolve. Accept either a build error or zero addresses: both mean the
	// channel would serve every RPC from an empty picker.
	badURL, err := url.Parse("dns-poll://localhost:50051")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	cc := &stubClientConn{}
	r, err := b.Build(resolver.Target{URL: *badURL}, cc, resolver.BuildOptions{})
	if err != nil {
		return
	}
	defer r.Close()

	time.Sleep(time.Second)
	if n := cc.addressCount(); n != 0 {
		t.Errorf("addressCount() = %d, want 0 for authority-slot target", n)
	}
}
