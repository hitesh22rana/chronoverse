package executor

import (
	"context"
	"encoding/json"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

const (
	heartbeatWorkflowDefaultRequestTimeout = 10 * time.Second
	heartbeatWorkflowMaxRequestTimeout     = 5 * time.Minute
)

type heartbeatDetails struct {
	TimeOut            time.Duration
	Endpoint           string
	ExpectedStatusCode int
	Headers            map[string][]string
}

// executeHeartbeatWorkflow executes the HEARTBEAT workflow.
func (r *Repository) executeHeartbeatWorkflow(ctx context.Context, jobID string, workflow *workflowspb.GetWorkflowByIDResponse) error {
	details, err := extractHeartbeatDetails(workflow.GetPayload())
	if err != nil {
		return err
	}

	//nolint:errcheck // Ignore the error as we don't want to block the job execution
	withRetry(func() error {
		// Issue necessary headers and tokens for authorization
		// This context uses the parent context
		//nolint:errcheck // Ignore the error as we don't want to block the job execution
		jobCtx, _ := r.withAuthorization(ctx)
		// Update the job status with the container ID
		if _, err := r.svc.Jobs.UpdateJobStatus(jobCtx, &jobspb.UpdateJobStatusRequest{
			Id:     jobID,
			Status: jobsmodel.JobStatusRunning.ToString(),
		}); err != nil {
			return status.Errorf(codes.Internal, "failed to update job status: %v", err)
		}
		return nil
	})

	return r.svc.Hsvc.Execute(ctx, details.TimeOut, details.Endpoint, details.ExpectedStatusCode, details.Headers)
}

// extractHeartbeatDetails extracts the heartbeat details from the workflow payload.
//
//nolint:gocyclo // This function is responsible for parsing the JSON payload and validating the fields.
func extractHeartbeatDetails(payload string) (*heartbeatDetails, error) {
	var (
		details = &heartbeatDetails{
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
		headersRaw, ok := data["headers"].(map[string]any)
		if !ok {
			return details, status.Errorf(codes.InvalidArgument, "invalid headers format")
		}

		headers = make(map[string][]string)
		for k, v := range headersRaw {
			switch val := v.(type) {
			case []any:
				strValues := make([]string, len(val))
				for i, iv := range val {
					strValues[i], ok = iv.(string)
					if !ok {
						return details, status.Errorf(codes.InvalidArgument, "header value must be string")
					}
				}
				headers[k] = strValues
			case string:
				headers[k] = []string{val}
			default:
				return details, status.Errorf(codes.InvalidArgument, "invalid header value for %s", k)
			}
		}
	}
	details.Headers = headers

	return details, nil
}
