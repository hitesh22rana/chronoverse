package heartbeat

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"golang.org/x/net/http/httpproxy"
	xproxy "golang.org/x/net/proxy"

	otelpkg "github.com/hitesh22rana/chronoverse/internal/pkg/otel"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

// dialerFactory creates the net.Dialer used for outbound heartbeat requests.
// The production factory enforces the egress guard; tests may inject a
// permissive factory to exercise plain HTTP semantics against local servers.
type dialerFactory func(requestTimeout time.Duration) *net.Dialer

// targetResolver resolves and validates a request's original destination. The
// returned addresses are safe to pin into a proxied request so the proxy never
// performs a second, potentially rebound or split-horizon DNS lookup. Tests may
// inject a permissive resolver to exercise local servers.
type targetResolver func(ctx context.Context, host string) ([]net.IPAddr, error)

// proxyResolver maps a request URL to the proxy that should carry it
// (nil means direct). The default implementation applies the standard
// environment semantics (HTTP_PROXY/HTTPS_PROXY/NO_PROXY, including their
// loopback bypass); tests may inject a fixed resolver to exercise proxying
// against local servers, which the environment semantics would never proxy.
type proxyResolver func(*url.URL) (*url.URL, error)

const (
	httpScheme  = "http"
	httpsScheme = "https"
)

func resolveProxyFromEnvironment(u *url.URL) (*url.URL, error) {
	return httpproxy.FromEnvironment().ProxyFunc()(u)
}

// Option configures a HeartBeat workflow.
type Option func(*HeartBeat)

// WithDialerFactory overrides the outbound dialer factory.
func WithDialerFactory(factory dialerFactory) Option {
	return func(h *HeartBeat) {
		if factory != nil {
			h.dialerFor = factory
		}
	}
}

// WithProxyResolver overrides how request URLs map to proxies.
func WithProxyResolver(resolver proxyResolver) Option {
	return func(h *HeartBeat) {
		if resolver != nil {
			h.proxyFor = resolver
		}
	}
}

// WithTargetResolver overrides destination resolution and validation.
func WithTargetResolver(resolver targetResolver) Option {
	return func(h *HeartBeat) {
		if resolver != nil {
			h.resolveTarget = resolver
		}
	}
}

// HeartBeat represents the HEARTBEAT workflow.
type HeartBeat struct {
	dialerFor     dialerFactory
	resolveTarget targetResolver
	proxyFor      proxyResolver
}

// New creates a new HEARTBEAT workflow.
func New(options ...Option) *HeartBeat {
	h := &HeartBeat{dialerFor: newGuardedDialer, resolveTarget: resolveEndpointHost, proxyFor: resolveProxyFromEnvironment}
	for _, option := range options {
		if option != nil {
			option(h)
		}
	}
	return h
}

// heartbeatTransport resolves and validates the original destination before
// every round trip. Direct requests retain the dial-time Control guard. When a
// proxy is selected, the request is pinned to a validated IP while preserving
// the original Host header and TLS server name; this prevents the proxy from
// resolving an attacker-controlled hostname to a different private address.
// Proxy selection is repeated for every redirect hop.
type heartbeatTransport struct {
	direct         *http.Transport
	requestTimeout time.Duration
	resolveTarget  targetResolver
	proxyFor       proxyResolver

	mu              sync.Mutex
	proxyTransports []*http.Transport
}

func newHeartbeatTransport(h *HeartBeat, timeout time.Duration) *heartbeatTransport {
	return &heartbeatTransport{
		direct:         newHTTPTransport(h.dialerFor(timeout)),
		requestTimeout: timeout,
		resolveTarget:  h.resolveTarget,
		proxyFor:       h.proxyFor,
	}
}

func (t *heartbeatTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	addresses, err := t.resolveTarget(req.Context(), req.URL.Hostname())
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrDisallowedTarget, req.URL.Hostname())
	}

	proxyURL, err := t.proxyFor(req.URL)
	if err != nil {
		return nil, fmt.Errorf("resolve proxy for %s: %w", req.URL.Redacted(), err)
	}
	if proxyURL == nil {
		return t.direct.RoundTrip(req)
	}

	var attemptErrors []error
	for _, address := range addresses {
		targetAddress, targetErr := pinnedTargetAddress(req.URL, address)
		if targetErr != nil {
			return nil, targetErr
		}
		transport, transportErr := t.newProxyTransport(proxyURL, targetAddress)
		if transportErr != nil {
			return nil, transportErr
		}
		t.mu.Lock()
		t.proxyTransports = append(t.proxyTransports, transport)
		t.mu.Unlock()

		response, roundTripErr := transport.RoundTrip(req)
		if roundTripErr == nil {
			return response, nil
		}
		transport.CloseIdleConnections()
		attemptErrors = append(attemptErrors, fmt.Errorf("%s: %w", targetAddress, roundTripErr))
		if req.Context().Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("all validated proxy destinations failed: %w", errors.Join(attemptErrors...))
}

func (t *heartbeatTransport) newProxyTransport(proxyURL *url.URL, targetAddress string) (*http.Transport, error) {
	dialer := &net.Dialer{Timeout: dialTimeoutFor(t.requestTimeout)}
	transport := newHTTPTransport(dialer)
	tunnelDial, err := proxyTunnelDialContext(proxyURL, targetAddress, dialer)
	if err != nil {
		return nil, err
	}
	// The transport sees a direct connection to the original URL, but the
	// dialer has already established a proxy tunnel to the validated IP. That
	// preserves the origin Host header and TLS SNI/certificate verification.
	transport.DialContext = tunnelDial
	return transport, nil
}

func (t *heartbeatTransport) CloseIdleConnections() {
	t.direct.CloseIdleConnections()
	t.mu.Lock()
	transports := append([]*http.Transport(nil), t.proxyTransports...)
	t.proxyTransports = nil
	t.mu.Unlock()
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

func newHTTPTransport(dialer *net.Dialer) *http.Transport {
	return &http.Transport{
		DialContext:         dialer.DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 5 * time.Second,
		IdleConnTimeout:     30 * time.Second,
		MaxIdleConns:        2,
	}
}

func pinnedTargetAddress(targetURL *url.URL, address net.IPAddr) (string, error) {
	if address.IP == nil {
		return "", fmt.Errorf("%w: %s", ErrDisallowedTarget, targetURL.Hostname())
	}
	port := targetURL.Port()
	if port == "" {
		switch targetURL.Scheme {
		case httpScheme:
			port = "80"
		case httpsScheme:
			port = "443"
		default:
			return "", fmt.Errorf("unsupported protocol scheme %q", targetURL.Scheme)
		}
	}
	ipHost := address.IP.String()
	if address.Zone != "" {
		ipHost += "%" + address.Zone
	}
	return net.JoinHostPort(ipHost, port), nil
}

func proxyTunnelDialContext(
	proxyURL *url.URL,
	targetAddress string,
	dialer *net.Dialer,
) (func(context.Context, string, string) (net.Conn, error), error) {
	proxyAddress, err := canonicalProxyAddress(proxyURL)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case httpScheme, httpsScheme, "":
		return func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialHTTPProxyTunnel(ctx, network, proxyURL, proxyAddress, targetAddress, dialer)
		}, nil
	case "socks5", "socks5h":
		var auth *xproxy.Auth
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			auth = &xproxy.Auth{User: proxyURL.User.Username(), Password: password}
		}
		socksDialer, err := xproxy.SOCKS5("tcp", proxyAddress, auth, dialer)
		if err != nil {
			return nil, fmt.Errorf("configure SOCKS proxy: %w", err)
		}
		contextDialer, ok := socksDialer.(xproxy.ContextDialer)
		if !ok {
			return nil, errors.New("SOCKS proxy dialer does not support context cancellation")
		}
		return func(ctx context.Context, network, _ string) (net.Conn, error) {
			return contextDialer.DialContext(ctx, network, targetAddress)
		}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", proxyURL.Scheme)
	}
}

func canonicalProxyAddress(proxyURL *url.URL) (string, error) {
	if proxyURL == nil || proxyURL.Hostname() == "" {
		return "", errors.New("proxy URL is missing a host")
	}
	port := proxyURL.Port()
	if port == "" {
		switch strings.ToLower(proxyURL.Scheme) {
		case httpsScheme:
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		case httpScheme, "":
			port = "80"
		default:
			return "", fmt.Errorf("unsupported proxy scheme %q", proxyURL.Scheme)
		}
	}
	return net.JoinHostPort(proxyURL.Hostname(), port), nil
}

func dialHTTPProxyTunnel(
	ctx context.Context,
	network string,
	proxyURL *url.URL,
	proxyAddress string,
	targetAddress string,
	dialer *net.Dialer,
) (net.Conn, error) {
	var (
		conn net.Conn
		err  error
	)
	if strings.EqualFold(proxyURL.Scheme, "https") {
		handshakeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tlsDialer := &tls.Dialer{
			NetDialer: dialer,
			Config: &tls.Config{
				ServerName: proxyURL.Hostname(),
				MinVersion: tls.VersionTLS12,
			},
		}
		conn, err = tlsDialer.DialContext(handshakeCtx, network, proxyAddress)
	} else {
		conn, err = dialer.DialContext(ctx, network, proxyAddress)
	}
	if err != nil {
		return nil, fmt.Errorf("dial proxy %s: %w", proxyURL.Redacted(), err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = conn.Close()
		}
	}()

	if deadline, ok := ctx.Deadline(); ok {
		if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
			return nil, deadlineErr
		}
	}
	stopCancel := context.AfterFunc(ctx, func() {
		if deadlineErr := conn.SetDeadline(time.Now()); deadlineErr != nil {
			return
		}
	})
	defer stopCancel()

	connectRequest := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: targetAddress},
		Host:   targetAddress,
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		credentials := proxyURL.User.Username() + ":" + password
		connectRequest.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	}
	if writeErr := connectRequest.Write(conn); writeErr != nil {
		return nil, fmt.Errorf("write proxy CONNECT: %w", writeErr)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, connectRequest)
	if err != nil {
		return nil, fmt.Errorf("read proxy CONNECT response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		if closeErr := response.Body.Close(); closeErr != nil {
			return nil, fmt.Errorf("proxy CONNECT failed: %s: close response: %w", response.Status, closeErr)
		}
		return nil, fmt.Errorf("proxy CONNECT failed: %s", response.Status)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	succeeded = true
	if reader.Buffered() != 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

// resolveEndpointHost applies the egress policy to every address the host
// currently resolves to and returns that exact validated result for optional
// proxy pinning. IP-literal hosts skip resolution. Resolution failures are
// returned as-is; only policy violations carry ErrDisallowedTarget.
func resolveEndpointHost(ctx context.Context, host string) ([]net.IPAddr, error) {
	if host == "" {
		return nil, fmt.Errorf("%w: missing host", ErrDisallowedTarget)
	}
	if ip := net.ParseIP(host); ip != nil {
		if isDisallowedIP(ip) {
			return nil, fmt.Errorf("%w: %s", ErrDisallowedTarget, ip)
		}
		return []net.IPAddr{{IP: ip}}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrDisallowedTarget, host)
	}
	for _, addr := range addrs {
		if isDisallowedIP(addr.IP) {
			return nil, fmt.Errorf("%w: %s", ErrDisallowedTarget, addr.IP)
		}
	}
	return addrs, nil
}

// Execute executes the HEARTBEAT workflow.
func (h *HeartBeat) Execute(
	ctx context.Context,
	timeout time.Duration,
	endpoint string,
	expectedStatusCode int,
	headers map[string][]string,
) error {
	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to create request: %v", err)
	}

	// Add headers
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Resolve and validate every destination before dispatch. Direct sockets
	// retain the dial-time guard; proxied requests are pinned to that validated
	// address so the proxy cannot perform a second, unsafe DNS resolution.
	transport := newHeartbeatTransport(h, timeout)
	client := &http.Client{
		Timeout:   timeout,
		Transport: otelpkg.HTTPTransport(transport),
	}
	defer transport.CloseIdleConnections()

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && errors.Is(urlErr.Err, ErrDisallowedTarget) {
			return status.Errorf(codes.FailedPrecondition, "endpoint is not allowed: %v", ErrDisallowedTarget)
		}
		if errors.Is(err, ErrDisallowedTarget) {
			return status.Errorf(codes.FailedPrecondition, "endpoint is not allowed: %v", ErrDisallowedTarget)
		}
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			return terminalreason.Wrap(terminalreason.TimeLimitExceeded, err)
		}
		return status.Errorf(codes.Unavailable, "failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	// Check for expected status code
	if resp.StatusCode != expectedStatusCode {
		return terminalreason.Wrap(terminalreason.UnexpectedStatusCode, status.Errorf(codes.FailedPrecondition, "unexpected status code: got %d, want %d", resp.StatusCode, expectedStatusCode))
	}

	return nil
}
