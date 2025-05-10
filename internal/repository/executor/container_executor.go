package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

const (
	containerWorkflowDefaultExecutionTimeout = 10 * time.Second
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

	logs, errs, workflowErr := r.svc.Csvc.Execute(
		ctx,
		details.TimeOut,
		details.Image,
		details.Cmd,
		details.Env,
	)

	// If there was an error starting the container, return immediately
	if workflowErr != nil {
		return workflowErr
	}

	var sequenceNum uint32

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

				currentSeq := atomic.AddUint32(&sequenceNum, 1)

				// Serialize the log entry
				jobEntryBytes, err := json.Marshal(&jobsmodel.JobLogEntry{
					JobID:       jobID,
					WorkflowID:  workflowID,
					UserID:      userID,
					Message:     log,
					TimeStamp:   time.Now(),
					SequenceNum: currentSeq,
				})
				if err != nil {
					continue
				}

				// Asynchronously produce the log entry to the Kafka topic
				r.kfk.Produce(ctx, kgo.SliceRecord(jobEntryBytes), func(_ *kgo.Record, _ error) {})

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

	image, ok := data["image"].(string)
	if !ok || image == "" {
		return details, status.Error(codes.InvalidArgument, "image is missing or invalid")
	}
	details.Image = image

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
