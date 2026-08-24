package heartbeat

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	otelpkg "github.com/hitesh22rana/chronoverse/internal/pkg/otel"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

// dialerFactory creates the net.Dialer used for outbound heartbeat requests.
// The production factory enforces the egress guard; tests may inject a
// permissive factory to exercise plain HTTP semantics against local servers.
type dialerFactory func(requestTimeout time.Duration) *net.Dialer

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

// HeartBeat represents the HEARTBEAT workflow.
type HeartBeat struct {
	dialerFor dialerFactory
}

// New creates a new HEARTBEAT workflow.
func New(options ...Option) *HeartBeat {
	h := &HeartBeat{dialerFor: newGuardedDialer}
	for _, option := range options {
		if option != nil {
			option(h)
		}
	}
	return h
}

// Execute executes the HEARTBEAT workflow.
func (h *HeartBeat) Execute(
	ctx context.Context,
	timeout time.Duration,
	endpoint string,
	expectedStatusCode int,
	headers map[string][]string,
) error {
	// HTTP client with timeout and an egress guard. The guard validates the
	// resolved destination address at dial time so redirects and DNS rebinding
	// cannot reach protected networks.
	client := &http.Client{
		Timeout: timeout,
		Transport: otelpkg.HTTPTransport(&http.Transport{
			DialContext:         h.dialerFor(timeout).DialContext,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 5 * time.Second,
		}),
	}

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
