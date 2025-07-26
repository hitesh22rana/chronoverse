package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

const (
	containerWorkflowDefaultExecutionTimeout = 30 * time.Second
	containerWorkflowMaxExecutionTimeout     = 1 * time.Hour
	containerBufferTimeout                   = 1 * time.Second
)

type containerDetails struct {
	TimeOut time.Duration
	Image   string
	Cmd     []string
	Env     []string
}

// executeContainerWorkflow executes the CONTAINER workflow.
func (r *Repository) executeContainerWorkflow(ctx context.Context, jobID string, workflow *workflowspb.GetWorkflowByIDResponse) error {
	workflowID := workflow.GetId()
	userID := workflow.GetUserId()

	details, err := extractContainerDetails(workflow.GetPayload())
	if err != nil {
		return err
	}

	containerID, logs, errs, workflowErr := r.svc.Csvc.Execute(
		ctx,
		details.TimeOut+containerBufferTimeout,
		details.Image,
		details.Cmd,
		details.Env,
	)

	// If there was an error starting the container, return immediately
	if workflowErr != nil {
		return workflowErr
	}

	// If the container ID is empty, return immediately
	if containerID == "" {
		return status.Error(codes.Aborted, "container ID is empty")
	}

	//nolint:errcheck // Ignore the error as we don't want to block the job execution
	withRetry(func() error {
		// Issue necessary headers and tokens for authorization
		// This context uses the parent context
		//nolint:errcheck // Ignore the error as we don't want to block the job execution
		jobCtx, _ := r.withAuthorization(ctx)
		// Update the job status with the container ID
		if _, err := r.svc.Jobs.UpdateJobStatus(jobCtx, &jobspb.UpdateJobStatusRequest{
			Id:          jobID,
			ContainerId: containerID,
			Status:      jobsmodel.JobStatusRunning.ToString(),
		}); err != nil {
			return status.Errorf(codes.Internal, "failed to update job status: %v", err)
		}
		return nil
	})

	// Create a done channel to signal when to stop processing
	done := make(chan struct{})
	defer close(done)

	// Process logs
	logsProcessing := make(chan struct{})
	go func() {
		defer close(logsProcessing)

		for {
			select {
			case log, ok := <-logs:
				if !ok {
					// Logs channel closed, we're done
					return
				}

				// Serialize the log entry
				jobLogEventBytes, err := json.Marshal(&jobsmodel.JobLogEvent{
					JobID:       jobID,
					WorkflowID:  workflowID,
					UserID:      userID,
					Message:     log.Message,
					TimeStamp:   log.Timestamp,
					SequenceNum: log.SequenceNum,
					Stream:      log.Stream,
				})
				if err != nil {
					continue
				}

				record := &kgo.Record{
					Topic: kafka.TopicJobLogs,
					Key:   []byte(jobID),
					Value: jobLogEventBytes,
				}

				// Asynchronously produce the record to Kafka
				r.kfk.Produce(context.WithoutCancel(ctx), record, func(_ *kgo.Record, _ error) {})

			case <-done:
				// We were signaled to stop processing logs
				return
			}
		}
	}()

	// Handle errors from the logs channel
	// This way we can immediately return when an error occurs
	for err := range errs {
		return err
	}

	<-logsProcessing
	return nil
}

// extractContainerDetails extracts the container details from the workflow payload.
func extractContainerDetails(payload string) (*containerDetails, error) {
	var (
		details = &containerDetails{
			TimeOut: containerWorkflowDefaultExecutionTimeout,
			Image:   "",
			Cmd:     []string{},
			Env:     []string{},
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

	if details.TimeOut > containerWorkflowMaxExecutionTimeout {
		return details, status.Error(codes.FailedPrecondition, "timeout exceeds maximum limit of 1 hour")
	}

	image, ok := data["image"].(string)
	if !ok || image == "" {
		return details, status.Error(codes.InvalidArgument, "image is missing or invalid")
	}
	details.Image = image

	// Command is an optional field
	cmd, ok := data["cmd"].([]any)
	if ok {
		// If cmd is provided, convert all elements to strings
		for _, c := range cmd {
			cStr, _ok := c.(string)
			if !_ok {
				return details, status.Error(codes.InvalidArgument, "cmd contains non-string elements")
			}
			details.Cmd = append(details.Cmd, cStr)
		}
	}

	// Environment variables are optional
	env, ok := data["env"].(map[string]any)
	if ok {
		// Convert the map to a slice of strings
		for key, value := range env {
			valueStr, _ok := value.(string)
			if !_ok {
				return details, status.Error(codes.InvalidArgument, "env contains non-string values")
			}
			details.Env = append(details.Env, fmt.Sprintf("%s=%s", key, valueStr))
		}
	}

	return details, nil
}
