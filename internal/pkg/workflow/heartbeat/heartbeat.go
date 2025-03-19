package heartbeat

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	timeout = 5 * time.Second
)

// HeartBeat represents the HEARTBEAT workflow.
type HeartBeat struct{}

// New creates a new HEARTBEAT workflow.
func New() *HeartBeat {
	return &HeartBeat{}
}

func validatePayload(data string) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to unmarshal payload: %v", err)
	}

	return payload, nil
}

// Execute executes the HEARTBEAT workflow.
//
//nolint:gocyclo // This function is not complex enough to warrant a refactor.
func (h *HeartBeat) Execute(ctx context.Context, payload string) error {
	// Validate payload
	data, err := validatePayload(payload)
	if err != nil {
		return err
	}

	// Validate endpoint
	if data["endpoint"] == nil {
		return status.Errorf(codes.InvalidArgument, "missing endpoint")
	}
	endpoint, ok := data["endpoint"].(string)
	if !ok {
		return status.Errorf(codes.InvalidArgument, "invalid endpoint: %v", data["endpoint"])
	}

	// Parse headers
	var headers map[string][]string
	if data["headers"] != nil {
		headersRaw, ok := data["headers"].(map[string]any)
		if !ok {
			return status.Errorf(codes.InvalidArgument, "invalid headers format")
		}

		headers = make(map[string][]string)
		for k, v := range headersRaw {
			switch val := v.(type) {
			case []any:
				strValues := make([]string, len(val))
				for i, iv := range val {
					strValues[i], ok = iv.(string)
					if !ok {
						return status.Errorf(codes.InvalidArgument, "header value must be string")
					}
				}
				headers[k] = strValues
			case string:
				headers[k] = []string{val}
			default:
				return status.Errorf(codes.InvalidArgument, "invalid header value for %s", k)
			}
		}
	}

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

	// Check for non-successful response
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return status.Errorf(
			codes.Unavailable,
			"received non-success response: %d %s",
			resp.StatusCode, resp.Status,
		)
	}

	return nil
}
