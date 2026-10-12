//nolint:testpackage // Tests exercise private transport and DNS safety boundaries.
package heartbeat

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	testSOCKS5Scheme  = "socks5"
	testSOCKS5HScheme = "socks5h"
)

func TestProxyTunnelConnectResponses(t *testing.T) {
	for _, tc := range []struct{ name, response, want string }{
		{"success", "HTTP/1.1 200 Connection established\r\n\r\npinned payload", ""},
		{"rejected", "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n", "407"},
		{"malformed", "not HTTP\r\n\r\n", "read proxy CONNECT response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			request := make(chan *http.Request, 1)
			done := make(chan error, 1)
			go func() { done <- serveCONNECTResponse(listener, tc.response, request) }()

			proxyURL := &url.URL{Scheme: "http", Host: listener.Addr().String(), User: url.UserPassword("user", "secret")}
			dial, err := proxyTunnelDialContext(proxyURL, "203.0.113.9:443", &net.Dialer{Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, err := dial(ctx, "tcp", "ignored.example:80")
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error = %v, want %s", err, tc.want)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				payload, readErr := io.ReadAll(conn)
				if readErr != nil || string(payload) != "pinned payload" {
					t.Fatalf("payload %q, error %v", payload, readErr)
				}
			}
			var req *http.Request
			select {
			case req = <-request:
			case <-time.After(3 * time.Second):
				t.Fatal("proxy did not receive a valid CONNECT request")
			}
			assertPinnedCONNECT(t, req)
			if serverErr := <-done; serverErr != nil {
				t.Fatal(serverErr)
			}
		})
	}
}

func assertPinnedCONNECT(t *testing.T, req *http.Request) {
	t.Helper()
	if req.Method != http.MethodConnect || req.Host != "203.0.113.9:443" || req.Header.Get("Proxy-Authorization") != "Basic dXNlcjpzZWNyZXQ=" {
		t.Fatalf("incorrect CONNECT: %#v", req)
	}
}

func serveCONNECTResponse(listener net.Listener, response string, request chan<- *http.Request) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadlineErr := conn.SetDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
		return deadlineErr
	}
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return err
	}
	request <- req
	_, err = io.WriteString(conn, response)
	return err
}

func TestProxyTunnelConfigurationAndCancellation(t *testing.T) {
	for _, proxyURL := range []*url.URL{nil, {Scheme: "http"}, {Scheme: "ftp", Host: "proxy.example"}, {Scheme: "ftp", Host: "proxy.example:21"}} {
		if _, err := proxyTunnelDialContext(proxyURL, "203.0.113.9:443", &net.Dialer{}); err == nil {
			t.Fatalf("accepted invalid proxy %v", proxyURL)
		}
	}
	for _, scheme := range []string{"http", "https", testSOCKS5Scheme, testSOCKS5HScheme} {
		t.Run(scheme, func(t *testing.T) {
			proxyURL := &url.URL{Scheme: scheme, Host: "127.0.0.1:1", User: url.UserPassword("user", "secret")}
			dial, err := proxyTunnelDialContext(proxyURL, "203.0.113.9:443", &net.Dialer{})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			conn, err := dial(ctx, "tcp", "ignored:80")
			if conn != nil {
				conn.Close()
				t.Fatal("canceled dial returned connection")
			}
			if err == nil {
				t.Fatal("canceled dial succeeded")
			}
		})
	}
}

func TestProxyTunnelWaitingResponseCanceled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer conn.Close()
		if deadlineErr := conn.SetDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
			done <- deadlineErr
			return
		}
		_, e = http.ReadRequest(bufio.NewReader(conn))
		if e != nil {
			done <- e
			return
		}
		close(received)
		_, e = io.Copy(io.Discard, conn)
		done <- e
	}()
	proxyURL := &url.URL{Scheme: "http", Host: listener.Addr().String()}
	dial, err := proxyTunnelDialContext(proxyURL, "203.0.113.9:443", &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		conn, e := dial(ctx, "tcp", "")
		if conn != nil {
			conn.Close()
		}
		result <- e
	}()
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("CONNECT was not received")
	}
	cancel()
	select {
	case e := <-result:
		if e == nil {
			t.Fatal("canceled tunnel succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not stop CONNECT")
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

func useControlledEndpointDNS(t *testing.T) {
	t.Helper()
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 1500)
		for {
			n, peer, e := listener.ReadFrom(buffer)
			if e != nil {
				return
			}
			var message dnsmessage.Message
			if message.Unpack(buffer[:n]) != nil {
				continue
			}
			message.Header.Response = true
			message.Header.RecursionAvailable = true
			for i := range message.Questions {
				q := &message.Questions[i]
				if q.Type != dnsmessage.TypeA {
					continue
				}
				ips := [][4]byte{{8, 8, 8, 8}, {1, 1, 1, 1}}
				if q.Name.String() == "unsafe.test." {
					ips = append(ips, [4]byte{127, 0, 0, 1})
				}
				for _, ip := range ips {
					message.Answers = append(message.Answers, dnsmessage.Resource{
						Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
						Body:   &dnsmessage.AResource{A: ip},
					})
				}
			}
			response, e := message.Pack()
			if e == nil {
				if _, writeErr := listener.WriteTo(response, peer); writeErr != nil {
					return
				}
			}
		}
	}()
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Resolver: original}).DialContext(ctx, "udp", listener.LocalAddr().String())
	}}
	t.Cleanup(func() { net.DefaultResolver = original; listener.Close(); <-done })
}

func TestResolveEndpointHostWithControlledDNS(t *testing.T) {
	useControlledEndpointDNS(t)
	for _, tc := range []struct {
		host string
		want []string
	}{
		{"8.8.8.8", []string{"8.8.8.8"}},
		{"2606:4700:4700::1111", []string{"2606:4700:4700::1111"}},
		{"safe.test", []string{"8.8.8.8", "1.1.1.1"}},
	} {
		assertResolvedEndpoint(t, tc.host, tc.want)
	}

	for _, host := range []string{"", "127.0.0.1", "::1", "unsafe.test"} {
		addresses, e := resolveEndpointHost(context.Background(), host)
		if !errors.Is(e, ErrDisallowedTarget) || len(addresses) != 0 {
			t.Fatalf("%s: addresses %v, error %v", host, addresses, e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := resolveEndpointHost(ctx, "canceled.test"); !errors.Is(e, context.Canceled) || errors.Is(e, ErrDisallowedTarget) {
		t.Fatalf("DNS cancellation error = %v", e)
	}
}

func assertResolvedEndpoint(t *testing.T, host string, want []string) {
	t.Helper()
	addresses, err := resolveEndpointHost(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != len(want) {
		t.Fatalf("%s: addresses %v, want %v", host, addresses, want)
	}
	seen := make(map[string]bool)
	for _, address := range addresses {
		seen[address.IP.String()] = true
	}
	for _, address := range want {
		if !seen[address] {
			t.Fatalf("%s: missing %s in resolved addresses %v", host, address, addresses)
		}
	}
}

func TestProxyTunnelSOCKSPinsTarget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() { done <- serveSOCKSTunnel(listener) }()
	proxyURL := &url.URL{Scheme: testSOCKS5HScheme, Host: listener.Addr().String()}
	dial, err := proxyTunnelDialContext(proxyURL, "203.0.113.9:443", &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", "attacker.example:80")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

func TestProxyTunnelConnectionRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	proxyURL := &url.URL{Scheme: "http", Host: address}
	dial, err := proxyTunnelDialContext(proxyURL, "203.0.113.9:443", &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dial(context.Background(), "tcp", "ignored.example:80")
	if conn != nil {
		conn.Close()
		t.Fatal("closed proxy returned a connection")
	}
	var networkErr *net.OpError
	if !errors.As(err, &networkErr) || !strings.Contains(err.Error(), "dial proxy") {
		t.Fatalf("network error = %v", err)
	}
}

func TestCanonicalProxyAddressDefaults(t *testing.T) {
	for _, tc := range []struct{ scheme, host, want string }{
		{"", "proxy.example", "proxy.example:80"},
		{"http", "proxy.example", "proxy.example:80"},
		{"HTTPS", "proxy.example", "proxy.example:443"},
		{testSOCKS5Scheme, "proxy.example", "proxy.example:1080"},
		{testSOCKS5HScheme, "proxy.example", "proxy.example:1080"},
		{"https", "[2001:db8::1]:8443", "[2001:db8::1]:8443"},
	} {
		t.Run(tc.scheme+tc.host, func(t *testing.T) {
			got, err := canonicalProxyAddress(&url.URL{Scheme: tc.scheme, Host: tc.host})
			if err != nil || got != tc.want {
				t.Fatalf("address = %q, error %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestPinnedTargetAddress(t *testing.T) {
	for _, tc := range []struct {
		scheme, port string
		address      net.IPAddr
		want         string
		fail         bool
	}{
		{scheme: "http", address: net.IPAddr{IP: net.ParseIP("8.8.8.8")}, want: "8.8.8.8:80"},
		{scheme: "https", address: net.IPAddr{IP: net.ParseIP("2606:4700:4700::1111")}, want: "[2606:4700:4700::1111]:443"},
		{scheme: "http", port: "8080", address: net.IPAddr{IP: net.ParseIP("fe80::1"), Zone: "test0"}, want: "[fe80::1%test0]:8080"},
		{scheme: "ftp", address: net.IPAddr{IP: net.ParseIP("8.8.8.8")}, fail: true},
		{scheme: "https", fail: true},
	} {
		t.Run(tc.scheme+tc.port+tc.want, func(t *testing.T) {
			host := "original.example"
			if tc.port != "" {
				host = net.JoinHostPort(host, tc.port)
			}
			got, err := pinnedTargetAddress(&url.URL{Scheme: tc.scheme, Host: host}, tc.address)
			if tc.fail {
				if err == nil {
					t.Fatal("invalid address accepted")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("target = %q, error %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestHTTPSProxyRequiresTrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Error("untrusted proxy received CONNECT") }))
	defer server.Close()
	proxyURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	dial, err := proxyTunnelDialContext(proxyURL, "203.0.113.9:443", &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", "ignored.example:80")
	if conn != nil {
		conn.Close()
		t.Fatal("untrusted proxy returned a connection")
	}
	var certErr x509.UnknownAuthorityError
	if !errors.As(err, &certErr) {
		t.Fatalf("certificate validation error = %v", err)
	}
}

func serveSOCKSTunnel(listener net.Listener) error {
	conn, e := listener.Accept()
	if e != nil {
		return e
	}
	defer conn.Close()
	if deadlineErr := conn.SetDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
		return deadlineErr
	}
	greeting := make([]byte, 3)
	if _, e = io.ReadFull(conn, greeting); e != nil {
		return e
	}
	if greeting[0] != 5 || greeting[1] != 1 || greeting[2] != 0 {
		return errors.New("unexpected SOCKS greeting")
	}
	if _, e = conn.Write([]byte{5, 0}); e != nil {
		return e
	}
	command := make([]byte, 10)
	if _, e = io.ReadFull(conn, command); e != nil {
		return e
	}
	want := []byte{5, 1, 0, 1, 203, 0, 113, 9, 1, 187}
	if !bytes.Equal(command, want) {
		return errors.New("SOCKS target is not pinned IP and port")
	}
	_, e = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	return e
}
