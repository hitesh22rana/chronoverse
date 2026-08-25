package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"golang.org/x/net/http/httpproxy"

	otelpkg "github.com/hitesh22rana/chronoverse/internal/pkg/otel"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

// dialerFactory creates the net.Dialer used for outbound heartbeat requests.
// The production factory enforces the egress guard; tests may inject a
// permissive factory to exercise plain HTTP semantics against local servers.
type dialerFactory func(requestTimeout time.Duration) *net.Dialer

// hostValidator validates a request's original destination host. The default
// implementation resolves the host and applies the egress policy to every
// returned address; tests may inject a stub to exercise proxy and redirect
// semantics against local servers.
type hostValidator func(ctx context.Context, host string) error

// proxyResolver maps a request URL to the proxy that should carry it
// (nil means direct). The default implementation applies the standard
// environment semantics (HTTP_PROXY/HTTPS_PROXY/NO_PROXY, including their
// loopback bypass); tests may inject a fixed resolver to exercise proxying
// against local servers, which the environment semantics would never proxy.
type proxyResolver func(*url.URL) (*url.URL, error)

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

// WithHostValidator overrides the destination-host validator.
func WithHostValidator(validator hostValidator) Option {
	return func(h *HeartBeat) {
		if validator != nil {
			h.validateHost = validator
		}
	}
}

// HeartBeat represents the HEARTBEAT workflow.
type HeartBeat struct {
	dialerFor    dialerFactory
	validateHost hostValidator
	proxyFor     proxyResolver
}

// New creates a new HEARTBEAT workflow.
func New(options ...Option) *HeartBeat {
	h := &HeartBeat{dialerFor: newGuardedDialer, validateHost: validateEndpointHost, proxyFor: resolveProxyFromEnvironment}
	for _, option := range options {
		if option != nil {
			option(h)
		}
	}
	return h
}

// validatingTransport enforces the egress policy on the ORIGINAL destination
// of every round trip before it is dispatched. This is required when an
// environment proxy is in play: the socket then connects to the proxy, so a
// dial-time guard would only inspect the proxy address. Running per round
// trip (rather than once per Execute) also covers every redirect hop.
type validatingTransport struct {
	inner    http.RoundTripper
	validate hostValidator
}

func (t *validatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.validate(req.Context(), req.URL.Hostname()); err != nil {
		return nil, err
	}
	return t.inner.RoundTrip(req)
}

// validateEndpointHost applies the egress policy to every address the host
// currently resolves to. IP-literal hosts skip resolution. Resolution
// failures are returned as-is (they surface as retryable transport errors);
// only policy violations carry ErrDisallowedTarget.
func validateEndpointHost(ctx context.Context, host string) error {
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrDisallowedTarget)
	}
	if ip := net.ParseIP(host); ip != nil {
		if isDisallowedIP(ip) {
			return fmt.Errorf("%w: %s", ErrDisallowedTarget, ip)
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%w: %s", ErrDisallowedTarget, host)
	}
	for _, addr := range addrs {
		if isDisallowedIP(addr.IP) {
			return fmt.Errorf("%w: %s", ErrDisallowedTarget, addr.IP)
		}
	}
	return nil
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

	// HTTP client with timeout and an egress guard. The guard validates the
	// original destination before every round trip (covering redirects and
	// the proxy case below) and again inside the dialer's Control hook so DNS
	// rebinding cannot reach protected networks on direct connections.
	transport := &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 5 * time.Second,
		IdleConnTimeout:     30 * time.Second,
		MaxIdleConns:        2,
	}
	proxyURL, proxyErr := h.proxyFor(req.URL)
	if proxyErr == nil && proxyURL != nil {
		// Environment proxy (HTTP_PROXY/HTTPS_PROXY/NO_PROXY): the socket
		// connects to the operator-configured proxy rather than the target,
		// so the guarded dialer would inspect the wrong address. Destination
		// policy is enforced by validatingTransport; the proxy address itself
		// is operator trust (it may legitimately be internal).
		transport.Proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
		transport.DialContext = (&net.Dialer{Timeout: dialTimeoutFor(timeout)}).DialContext
	} else {
		transport.DialContext = h.dialerFor(timeout).DialContext
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: &validatingTransport{inner: otelpkg.HTTPTransport(transport), validate: h.validateHost},
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
