package heartbeat

import (
	"encoding/json"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	heartbeatWorkflowDefaultRequestTimeout = 10 * time.Second
	heartbeatWorkflowMaxRequestTimeout     = 5 * time.Minute
)

// Details configures a heartbeat check: timeout, endpoint URL, expected status, and headers.
// to be included in the request.
type Details struct {
	TimeOut            time.Duration       // Maximum duration to wait for the heartbeat request to complete
	Endpoint           string              // URL of the endpoint to send the heartbeat request to
	ExpectedStatusCode int                 // HTTP status code expected in a successful heartbeat response
	Headers            map[string][]string // Custom headers to include in the heartbeat request
}

// ExtractAndValidateHeartbeatDetails extracts the heartbeat details from the workflow payload.
func ExtractAndValidateHeartbeatDetails(payload string) (*Details, error) {
	var (
		details = &Details{
			TimeOut:            heartbeatWorkflowDefaultRequestTimeout,
			Endpoint:           "",
			ExpectedStatusCode: 200,
			Headers:            map[string][]string{},
		}
		err  error
		data map[string]any
	)

	if err = json.Unmarshal([]byte(payload), &data); err != nil {
		return details, status.Error(codes.InvalidArgument, "invalid payload format")
	}

	timeout, ok := data["timeout"].(string)
	if ok {
		details.TimeOut, err = time.ParseDuration(timeout)
		if err != nil {
			return details, status.Error(codes.InvalidArgument, "timeout is invalid")
		}
	}

	if details.TimeOut <= 0 {
		return details, status.Error(codes.InvalidArgument, "timeout is invalid")
	}

	if details.TimeOut > heartbeatWorkflowMaxRequestTimeout {
		return details, status.Errorf(codes.FailedPrecondition, "timeout exceeds maximum limit of %.0f minutes", heartbeatWorkflowMaxRequestTimeout.Minutes())
	}

	if data["endpoint"] == nil {
		return details, status.Errorf(codes.InvalidArgument, "missing endpoint")
	}
	endpoint, ok := data["endpoint"].(string)
	if !ok {
		return details, status.Errorf(codes.InvalidArgument, "invalid endpoint: %v", data["endpoint"])
	}
	details.Endpoint = endpoint

	if data["expected_status_code"] != nil {
		expectedStatusCode, ok := data["expected_status_code"].(float64)
		if !ok {
			return details, status.Errorf(codes.InvalidArgument, "invalid expected status code: %v", data["expected_status_code"])
		}
		if expectedStatusCode < 100 || expectedStatusCode > 599 {
			return details, status.Errorf(codes.InvalidArgument, "expected status code must be between 100 and 599")
		}
		details.ExpectedStatusCode = int(expectedStatusCode)
	}

	var headers map[string][]string
	if data["headers"] != nil {
		var headersErr error
		headers, headersErr = parseHeartbeatHeaders(data["headers"])
		if headersErr != nil {
			return details, headersErr
		}
	}
	details.Headers = headers

	return details, nil
}
