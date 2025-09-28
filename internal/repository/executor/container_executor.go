package executor

import (
	"context"
	"encoding/json"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

// executeContainerWorkflow executes the CONTAINER workflow.
func (r *Repository) executeContainerWorkflow(ctx context.Context, jobID string, workflow *workflowspb.GetWorkflowByIDResponse) error {
	workflowID := workflow.GetId()
	userID := workflow.GetUserId()

	details, err := container.ExtractAndValidateContainerDetails(workflow.GetPayload())
	if err != nil {
		return err
	}

	containerID, logs, errs, workflowErr := r.svc.Csvc.Execute(
		ctx,
		details.TimeOut,
		details.Image,
		details.Cmd,
		details.Env,
	)

	// If there was an error starting the container, return immediately
	if status.Code(workflowErr) == codes.FailedPrecondition {
		return workflowErr
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

	// If there was an error during execution, return it
	if workflowErr != nil {
		return workflowErr
	}

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
