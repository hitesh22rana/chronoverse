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
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	xproxy "golang.org/x/net/proxy"
)

const (
	testSOCKS5Scheme  = "socks5"
	testSOCKS5HScheme = "socks5h"

	// Tunnel fixtures bypass destination validation and ignore the caller address.
	pinnedTarget = "203.0.113.9:443"

	// Bound fixture waits independently of the package timeout.
	fixtureTimeout = 3 * time.Second
)

// SOCKS5 CONNECT to pinnedTarget over IPv4, port 443.
var socks5PinnedCommand = []byte{5, 1, 0, 1, 203, 0, 113, 9, 1, 187}

func TestProxyTunnelConnectResponses(t *testing.T) {
	for _, tc := range []struct{ name, response, want, status string }{
		{"success", "HTTP/1.1 200 Connection established\r\n\r\npinned payload", "", ""},
		{"rejected", "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n", "407", ""},
		// Preserve status and close errors for truncated rejection bodies.
		{"rejected with truncated body", "HTTP/1.1 407 Proxy Authentication Required\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nshort", "close response", "407"},
		{"malformed", "not HTTP\r\n\r\n", "read proxy CONNECT response", ""},
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

			proxyURL := &url.URL{Scheme: httpScheme, Host: listener.Addr().String(), User: url.UserPassword("user", "secret")}
			dial, err := proxyTunnelDialContext(proxyURL, pinnedTarget, &net.Dialer{Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
			defer cancel()
			conn, err := dial(ctx, "tcp", "ignored.example:80")
			if tc.want != "" {
				assertProxyConnectError(t, err, tc.want, tc.status)
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
			case <-time.After(fixtureTimeout):
				t.Fatal("proxy did not receive a valid CONNECT request")
			}
			assertPinnedCONNECT(t, req)
			awaitTransportFixture(t, done)
		})
	}
}

func assertPinnedCONNECT(t *testing.T, req *http.Request) {
	t.Helper()
	if req.Method != http.MethodConnect || req.Host != pinnedTarget || req.Header.Get("Proxy-Authorization") != "Basic dXNlcjpzZWNyZXQ=" {
		t.Fatalf("incorrect CONNECT: %#v", req)
	}
}

func serveCONNECTResponse(listener net.Listener, response string, request chan<- *http.Request) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadlineErr := conn.SetDeadline(time.Now().Add(2 * fixtureTimeout)); deadlineErr != nil {
		return deadlineErr
	}
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return err
	}
	request <- req
	// A single write includes payload bytes in the CONNECT response buffer.
	_, err = io.WriteString(conn, response)
	return err
}

func TestProxyTunnelRejectsInvalidProxyURLs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		proxyURL *url.URL
	}{
		{"nil url", nil},
		{"missing host", &url.URL{Scheme: httpScheme}},
		{"unsupported scheme without port", &url.URL{Scheme: "ftp", Host: "proxy.example"}},
		{"unsupported scheme with port", &url.URL{Scheme: "ftp", Host: "proxy.example:21"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := proxyTunnelDialContext(tc.proxyURL, pinnedTarget, &net.Dialer{}); err == nil {
				t.Fatalf("accepted invalid proxy %v", tc.proxyURL)
			}
		})
	}
}

// TestProxyTunnelDialStopsOnCanceledContext asserts the context error itself.
// A bare "err != nil" would be satisfied by the refusal that 127.0.0.1:1
// produces anyway, which would hide a dialer that dropped the request context.
func TestProxyTunnelDialStopsOnCanceledContext(t *testing.T) {
	for _, scheme := range []string{httpScheme, httpsScheme, testSOCKS5Scheme, testSOCKS5HScheme} {
		t.Run(scheme, func(t *testing.T) {
			proxyURL := &url.URL{Scheme: scheme, Host: "127.0.0.1:1", User: url.UserPassword("user", "secret")}
			dial, err := proxyTunnelDialContext(proxyURL, pinnedTarget, &net.Dialer{})
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
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled dial error = %v, want context.Canceled", err)
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
		if deadlineErr := conn.SetDeadline(time.Now().Add(2 * fixtureTimeout)); deadlineErr != nil {
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
	proxyURL := &url.URL{Scheme: httpScheme, Host: listener.Addr().String()}
	dial, err := proxyTunnelDialContext(proxyURL, pinnedTarget, &net.Dialer{})
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
	case <-time.After(fixtureTimeout):
		t.Fatal("CONNECT was not received")
	}
	cancel()
	select {
	case e := <-result:
		if e == nil {
			t.Fatal("canceled tunnel succeeded")
		}
	case <-time.After(fixtureTimeout):
		t.Fatal("cancellation did not stop CONNECT")
	}
	awaitTransportFixture(t, done)
}

// SOCKS cancellation expires socket I/O. The fixture deadline exceeds the
// guard so ignoring cancellation cannot pass as an ordinary timeout.
func TestProxyTunnelSOCKSHandshakeCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	negotiated := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- serveStalledSOCKSTunnel(listener, negotiated, release) }()
	// Tear the socket down if an assertion gives up early, so the fixture
	// goroutine cannot outlive the test.
	t.Cleanup(func() { close(release) })

	proxyURL := &url.URL{Scheme: testSOCKS5HScheme, Host: listener.Addr().String()}
	dial, err := proxyTunnelDialContext(proxyURL, pinnedTarget, &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		conn, dialErr := dial(ctx, "tcp", "attacker.example:80")
		if conn != nil {
			conn.Close()
		}
		result <- dialErr
	}()
	select {
	case <-negotiated:
	case <-time.After(fixtureTimeout):
		t.Fatal("SOCKS handshake did not reach the CONNECT request")
	}
	cancel()
	select {
	case dialErr := <-result:
		// Accept either representation of "the context interrupted the
		// handshake"; the guard is what distinguishes it from the fixture's
		// own deadline.
		if !errors.Is(dialErr, os.ErrDeadlineExceeded) && !errors.Is(dialErr, context.Canceled) {
			t.Fatalf("SOCKS cancellation error = %v, want the context to interrupt the handshake", dialErr)
		}
	case <-time.After(fixtureTimeout):
		t.Fatal("cancellation did not stop the SOCKS handshake")
	}
	awaitTransportFixture(t, done)
}

// Unknown names fail with NXDOMAIN.
var controlledDNSAnswers = map[string][][4]byte{
	"safe.test.":   {{8, 8, 8, 8}, {1, 1, 1, 1}},
	"unsafe.test.": {{8, 8, 8, 8}, {1, 1, 1, 1}, {127, 0, 0, 1}},
}

// Sequential only: replaces net.DefaultResolver until cleanup.
// Go defers parallel tests until sequential tests finish.
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
			n, peer, readErr := listener.ReadFrom(buffer)
			if readErr != nil {
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
				ips, known := controlledDNSAnswers[q.Name.String()]
				if !known {
					message.Header.RCode = dnsmessage.RCodeNameError
					continue
				}
				// A known name with no IPv6 answer is NOERROR with no records,
				// which is how a dual-stack client learns to use the A records.
				if q.Type != dnsmessage.TypeA {
					continue
				}
				for _, ip := range ips {
					message.Answers = append(message.Answers, dnsmessage.Resource{
						Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
						Body:   &dnsmessage.AResource{A: ip},
					})
				}
			}
			response, packErr := message.Pack()
			if packErr != nil {
				continue
			}
			if _, writeErr := listener.WriteTo(response, peer); writeErr != nil {
				return
			}
		}
	}()
	original := net.DefaultResolver
	// Pin the old resolver: canceled lookups can leave background dial goroutines
	// that would otherwise race with restoring net.DefaultResolver.
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Resolver: original}).DialContext(ctx, "udp", listener.LocalAddr().String())
	}}
	t.Cleanup(func() {
		net.DefaultResolver = original
		listener.Close()
		<-done
	})
}

// Stay sequential while replacing net.DefaultResolver.
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
	dial, err := proxyTunnelDialContext(proxyURL, pinnedTarget, &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	conn, err := dial(ctx, "tcp", "attacker.example:80")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	awaitTransportFixture(t, done)
}

// serveStalledSOCKSTunnel completes authentication and the CONNECT request,
// signals negotiated, then never answers. Its socket deadline is deliberately
// twice fixtureTimeout so that TestProxyTunnelSOCKSHandshakeCancellation's
// guard, not the fixture, is what decides whether the context reached the
// dialer. release lets the test drop the socket so this goroutine cannot
// outlive it.
func serveStalledSOCKSTunnel(listener net.Listener, negotiated chan<- struct{}, release <-chan struct{}) error {
	conn, err := acceptSOCKS5Handshake(listener, 2*fixtureTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	stalled := make(chan struct{})
	defer close(stalled)
	go func() {
		select {
		case <-release:
			conn.Close()
		case <-stalled:
		}
	}()
	close(negotiated)
	_, err = io.Copy(io.Discard, conn)
	// The fixture's own deadline and the test's teardown both end this read.
	// Neither carries information about the code under test.
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
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
	proxyURL := &url.URL{Scheme: httpScheme, Host: address}
	dial, err := proxyTunnelDialContext(proxyURL, pinnedTarget, &net.Dialer{Timeout: time.Second})
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

// A SOCKS constructor that cannot fail, or that hands back a dialer without
// context support, is what the two guards in proxyTunnelDialContext are for.
// The pinned x/net never does either, so the constructor is replaced here.
func TestProxyTunnelRejectsUnusableSOCKSDialers(t *testing.T) {
	original := socks5Dialer
	t.Cleanup(func() { socks5Dialer = original })
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "construction fails", err: errors.New("proxy address rejected"), want: "configure SOCKS proxy: proxy address rejected"},
		{name: "no context support", want: "does not support context cancellation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socks5Dialer = func(string, string, *xproxy.Auth, xproxy.Dialer) (xproxy.Dialer, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return dialerWithoutContext{}, nil
			}
			proxyURL := &url.URL{Scheme: testSOCKS5Scheme, Host: "127.0.0.1:1"}
			dial, err := proxyTunnelDialContext(proxyURL, pinnedTarget, &net.Dialer{})
			if dial != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("dial returned = %v, error = %v; want %q", dial != nil, err, tc.want)
			}
		})
	}
}

// dialerWithoutContext is an xproxy.Dialer that predates the ContextDialer
// interface; the assertion on it must reject it rather than dial.
type dialerWithoutContext struct{}

func (dialerWithoutContext) Dial(string, string) (net.Conn, error) {
	return nil, errors.New("dialed")
}

// The three SetDeadline failures and the CONNECT write failure in
// dialHTTPProxyTunnel cannot happen on a TCP connection, so the proxy dial is
// replaced with one that hands back a connection that fails on demand. Each
// test also asserts the socket is closed, because that is what the tunnel's
// succeeded flag is for.
func TestDialHTTPProxyTunnelSocketFailures(t *testing.T) {
	original := dialProxyConn
	t.Cleanup(func() { dialProxyConn = original })
	proxyURL := &url.URL{Scheme: httpScheme, Host: "proxy.example:3128"}

	t.Run("caller deadline cannot be set", func(t *testing.T) {
		socket := &fakeProxyConn{deadlineErr: errFakeDeadline}
		useFakeProxyConn(t, original, socket)
		ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
		defer cancel()
		conn, err := dialHTTPProxyTunnel(ctx, "tcp", proxyURL, "proxy.example:3128", pinnedTarget, &net.Dialer{})
		assertFailedTunnel(t, conn, err, socket, errFakeDeadline)
	})

	t.Run("CONNECT cannot be written", func(t *testing.T) {
		socket := &fakeProxyConn{writeErr: errFakeWrite}
		useFakeProxyConn(t, original, socket)
		conn, err := dialHTTPProxyTunnel(context.Background(), "tcp", proxyURL, "proxy.example:3128", pinnedTarget, &net.Dialer{})
		assertFailedTunnel(t, conn, err, socket, errFakeWrite)
	})

	// Cancellation expires the socket, and the tunnel then clears the
	// deadline it set; both SetDeadline calls fail here, and the guard that
	// discards the AfterFunc's error is only visible as the call not
	// blocking the cancellation.
	t.Run("cancellation and reset cannot set a deadline", func(t *testing.T) {
		release := make(chan struct{})
		socket := &fakeProxyConn{
			deadlineErr: errFakeDeadline,
			reader:      strings.NewReader(connectEstablished),
			blocked:     release,
			deadlines:   make(chan struct{}, 2),
		}
		useFakeProxyConn(t, original, socket)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() {
			conn, err := dialHTTPProxyTunnel(ctx, "tcp", proxyURL, "proxy.example:3128", pinnedTarget, &net.Dialer{})
			if conn != nil {
				conn.Close()
			}
			result <- err
		}()
		cancel()
		select {
		case <-socket.deadlines:
		case <-time.After(fixtureTimeout):
			t.Fatal("cancellation did not reach the proxy socket")
		}
		close(release)
		select {
		case err := <-result:
			if !errors.Is(err, errFakeDeadline) {
				t.Fatalf("error = %v, want %v", err, errFakeDeadline)
			}
		case <-time.After(fixtureTimeout):
			t.Fatal("tunnel did not finish")
		}
		if !socket.closed {
			t.Fatal("failed tunnel left the proxy socket open")
		}
	})
}

// net.Resolver reports an answer with no records as "no such host", so the
// empty-answer guard in resolveEndpointHost only sees a resolver that returns
// none without complaining.
func TestResolveEndpointHostRejectsEmptyAnswer(t *testing.T) {
	original := lookupIPAddr
	t.Cleanup(func() { lookupIPAddr = original })
	lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) { return nil, nil }
	addresses, err := resolveEndpointHost(context.Background(), "empty.test")
	if !errors.Is(err, ErrDisallowedTarget) || len(addresses) != 0 {
		t.Fatalf("addresses = %v, error = %v", addresses, err)
	}
}

const connectEstablished = "HTTP/1.1 200 Connection established\r\n\r\n"

var (
	errFakeDeadline = errors.New("socket deadline rejected")
	errFakeWrite    = errors.New("socket write rejected")
)

// fakeProxyConn is the connection dialProxyConn hands back: every deadline
// fails, writes and reads fail on demand, and SetDeadline reports the attempt
// so a caller can wait for cancellation to reach the socket.
type fakeProxyConn struct {
	reader      io.Reader
	blocked     <-chan struct{}
	writeErr    error
	deadlineErr error
	deadlines   chan struct{}
	closed      bool
}

func (c *fakeProxyConn) Read(p []byte) (int, error) {
	if c.blocked != nil {
		<-c.blocked
	}
	if c.reader == nil {
		return 0, io.EOF
	}
	return c.reader.Read(p)
}

func (c *fakeProxyConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(p), nil
}

func (c *fakeProxyConn) Close() error {
	c.closed = true
	return nil
}

func (c *fakeProxyConn) LocalAddr() net.Addr             { return fakeProxyAddr{} }
func (c *fakeProxyConn) RemoteAddr() net.Addr            { return fakeProxyAddr{} }
func (c *fakeProxyConn) SetDeadline(time.Time) error     { return c.setDeadline() }
func (c *fakeProxyConn) SetReadDeadline(time.Time) error { return c.setDeadline() }

func (c *fakeProxyConn) SetWriteDeadline(time.Time) error { return c.setDeadline() }

func (c *fakeProxyConn) setDeadline() error {
	if c.deadlines != nil {
		select {
		case c.deadlines <- struct{}{}:
		default:
		}
	}
	return c.deadlineErr
}

type fakeProxyAddr struct{}

func (fakeProxyAddr) Network() string { return "tcp" }
func (fakeProxyAddr) String() string  { return "127.0.0.1:0" }

func useFakeProxyConn(t *testing.T, original func(context.Context, *net.Dialer, string, string) (net.Conn, error), socket net.Conn) {
	t.Helper()
	dialProxyConn = func(context.Context, *net.Dialer, string, string) (net.Conn, error) { return socket, nil }
	t.Cleanup(func() { dialProxyConn = original })
}

func assertFailedTunnel(t *testing.T, conn net.Conn, err error, socket *fakeProxyConn, want error) {
	t.Helper()
	if conn != nil {
		conn.Close()
		t.Fatal("failed CONNECT returned a connection")
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if !socket.closed {
		t.Fatal("failed tunnel left the proxy socket open")
	}
}

func TestCanonicalProxyAddressDefaults(t *testing.T) {
	for _, tc := range []struct{ scheme, host, want string }{
		{"", "proxy.example", "proxy.example:80"},
		{httpScheme, "proxy.example", "proxy.example:80"},
		{"HTTPS", "proxy.example", "proxy.example:443"},
		{testSOCKS5Scheme, "proxy.example", "proxy.example:1080"},
		{testSOCKS5HScheme, "proxy.example", "proxy.example:1080"},
		{httpsScheme, "[2001:db8::1]:8443", "[2001:db8::1]:8443"},
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
		{scheme: httpScheme, address: net.IPAddr{IP: net.ParseIP("8.8.8.8")}, want: "8.8.8.8:80"},
		{scheme: httpsScheme, address: net.IPAddr{IP: net.ParseIP("2606:4700:4700::1111")}, want: "[2606:4700:4700::1111]:443"},
		{scheme: httpScheme, port: "8080", address: net.IPAddr{IP: net.ParseIP("fe80::1"), Zone: "test0"}, want: "[fe80::1%test0]:8080"},
		{scheme: "ftp", address: net.IPAddr{IP: net.ParseIP("8.8.8.8")}, fail: true},
		{scheme: httpsScheme, fail: true},
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
	dial, err := proxyTunnelDialContext(proxyURL, pinnedTarget, &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
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
	conn, err := acceptSOCKS5Handshake(listener, 2*fixtureTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	return err
}

// acceptSOCKS5Handshake accepts one client, completes method negotiation, and
// reads the CONNECT request, failing unless the client asked for pinnedTarget
// rather than the address it handed to the dialer.
func acceptSOCKS5Handshake(listener net.Listener, timeout time.Duration) (net.Conn, error) {
	conn, err := listener.Accept()
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Conn, error) {
		conn.Close()
		return nil, err
	}
	if deadlineErr := conn.SetDeadline(time.Now().Add(timeout)); deadlineErr != nil {
		return fail(deadlineErr)
	}
	greeting := make([]byte, 3)
	if _, err = io.ReadFull(conn, greeting); err != nil {
		return fail(err)
	}
	if greeting[0] != 5 || greeting[1] != 1 || greeting[2] != 0 {
		return fail(errors.New("unexpected SOCKS greeting"))
	}
	if _, err = conn.Write([]byte{5, 0}); err != nil {
		return fail(err)
	}
	command := make([]byte, len(socks5PinnedCommand))
	if _, err = io.ReadFull(conn, command); err != nil {
		return fail(err)
	}
	if !bytes.Equal(command, socks5PinnedCommand) {
		return fail(errors.New("SOCKS target is not pinned IP and port"))
	}
	return conn, nil
}

func assertProxyConnectError(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected CONNECT error")
	}
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error = %v, want %q", err, fragment)
		}
	}
}

func awaitTransportFixture(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(fixtureTimeout):
		t.Fatal("transport fixture did not finish")
	}
}
