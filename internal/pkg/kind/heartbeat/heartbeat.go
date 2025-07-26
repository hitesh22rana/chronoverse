package heartbeat

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// HeartBeat represents the HEARTBEAT workflow.
type HeartBeat struct{}

// New creates a new HEARTBEAT workflow.
func New() *HeartBeat {
	return &HeartBeat{}
}

// Execute executes the HEARTBEAT workflow.
func (h *HeartBeat) Execute(
	ctx context.Context,
	timeout time.Duration,
	endpoint string,
	expectedStatusCode int,
	headers map[string][]string,
) error {
	// HTTP client with timeout
	client := &http.Client{
		Timeout: timeout,
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
		return status.Errorf(codes.Unavailable, "failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	// Check for expected status code
	if resp.StatusCode != expectedStatusCode {
		return status.Errorf(codes.FailedPrecondition, "unexpected status code: got %d, want %d", resp.StatusCode, expectedStatusCode)
	}

	return nil
}
