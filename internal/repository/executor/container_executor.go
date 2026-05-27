package executor

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

// executeContainerWorkflow executes the CONTAINER workflow.
func (r *Repository) executeContainerWorkflow(
	ctx context.Context,
	jobID,
	leaseToken string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
) (string, error) {
	details, err := container.ExtractAndValidateContainerDetails(workflow.GetPayload())
	if err != nil {
		return "", err
	}

	containerID, logs, errs, workflowErr := r.svc.Csvc.Execute(
		ctx,
		details.TimeOut,
		details.Image,
		details.Cmd,
		details.Env,
	)

	if status.Code(workflowErr) == codes.FailedPrecondition {
		return "", workflowErr
	}

	if containerID != "" {
		if err := r.attachJobContainer(ctx, jobID, leaseToken, containerID); err != nil {
			return containerID, err
		}
	}

	if workflowErr != nil {
		return containerID, workflowErr
	}

	return containerID, r.processContainerExecution(ctx, jobID, attempts, workflow, logs, errs)
}

func (r *Repository) attachJobContainer(ctx context.Context, jobID, leaseToken, containerID string) error {
	jobCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	_, err = r.svc.Jobs.AttachJobContainer(jobCtx, &jobspb.AttachJobContainerRequest{
		Id:          jobID,
		LeaseToken:  leaseToken,
		ContainerId: containerID,
	})
	return err
}

func (r *Repository) processContainerExecution(
	ctx context.Context,
	jobID string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
	logs <-chan *jobsmodel.JobLog,
	errs <-chan error,
) error {
	done := make(chan struct{})
	defer close(done)

	logErrors := r.publishContainerLogs(ctx, jobID, attempts, workflow, logs, done)
	execErrors := errs
	var execErr error
	for execErrors != nil || logErrors != nil {
		select {
		case err, ok := <-execErrors:
			if !ok {
				execErrors = nil
				continue
			}
			if execErr == nil {
				execErr = err
			}
		case err, ok := <-logErrors:
			if !ok {
				logErrors = nil
				continue
			}
			return err
		}
	}

	return execErr
}

func (r *Repository) publishContainerLogs(
	ctx context.Context,
	jobID string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
	logs <-chan *jobsmodel.JobLog,
	done <-chan struct{},
) <-chan error {
	logErrors := make(chan error, 1)
	go func() {
		defer close(logErrors)

		for {
			select {
			case log, ok := <-logs:
				if !ok {
					return
				}
				if err := r.publishContainerLog(ctx, jobID, attempts, workflow, log); err != nil {
					logErrors <- err
					return
				}
			case <-done:
				return
			}
		}
	}()

	return logErrors
}

func (r *Repository) publishContainerLog(
	ctx context.Context,
	jobID string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
	log *jobsmodel.JobLog,
) error {
	return r.enqueueJobLogEvent(ctx, &jobsmodel.JobLogEvent{
		EventKey:    idempotency.LogEventKey(jobID, log.Stream, log.SequenceNum, attempts),
		JobID:       jobID,
		WorkflowID:  workflow.GetId(),
		UserID:      workflow.GetUserId(),
		Message:     log.Message,
		TimeStamp:   log.Timestamp,
		SequenceNum: log.SequenceNum,
		Stream:      log.Stream,
		Retention:   workflow.GetLogRetention(),
	})
}

func (r *Repository) enqueueJobLogEvent(ctx context.Context, event *jobsmodel.JobLogEvent) error {
	if event == nil {
		return status.Error(codes.InvalidArgument, "job log event is required")
	}
	if event.EventKey == "" {
		event.EventKey = idempotency.LogEventKey(event.JobID, event.Stream, event.SequenceNum)
	}

	req := &jobspb.EnqueueJobLogRequest{
		EventKey:    event.EventKey,
		JobId:       event.JobID,
		WorkflowId:  event.WorkflowID,
		UserId:      event.UserID,
		Message:     event.Message,
		Timestamp:   event.TimeStamp.Format(time.RFC3339Nano),
		SequenceNum: event.SequenceNum,
		Stream:      event.Stream,
		Retention:   event.Retention,
	}

	for {
		if ctx.Err() != nil {
			return contextError(ctx.Err())
		}

		authCtx, err := r.withAuthorization(ctx)
		if err == nil {
			_, err = r.svc.Jobs.EnqueueJobLog(authCtx, req)
		}
		if err == nil {
			return nil
		}
		if !isRetryableJobLogEnqueueError(err) {
			return err
		}

		loggerpkg.FromContext(ctx).Warn("failed to enqueue job log; retrying",
			zap.String("job_id", event.JobID),
			zap.String("workflow_id", event.WorkflowID),
			zap.String("event_key", event.EventKey),
			zap.String("stream", event.Stream),
			zap.Uint32("sequence_num", event.SequenceNum),
			zap.String("code", status.Code(err).String()),
			zap.Error(err),
		)

		select {
		case <-time.After(retryBackoff):
		case <-ctx.Done():
			return contextError(ctx.Err())
		}
	}
}

func isRetryableJobLogEnqueueError(err error) bool {
	switch status.Code(err) { //nolint:exhaustive // Only non-retryable validation/auth codes need special handling here.
	case codes.InvalidArgument, codes.FailedPrecondition, codes.PermissionDenied, codes.Unauthenticated:
		return false
	default:
		return true
	}
}

func contextError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, err.Error())
	}

	return status.Error(codes.Canceled, err.Error())
}
