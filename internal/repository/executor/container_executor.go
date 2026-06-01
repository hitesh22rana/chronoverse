package executor

import (
	"context"
	"errors"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
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

		batchSize := r.cfg.JobLogBatchSize
		records := make([]*kgo.Record, 0, batchSize)
		ticker := time.NewTicker(r.cfg.JobLogBatchInterval)
		defer ticker.Stop()

		flush := func() {
			if len(records) == 0 {
				return
			}

			r.publishJobLogBatch(ctx, records)
			records = make([]*kgo.Record, 0, batchSize)
		}

		for {
			select {
			case log, ok := <-logs:
				if !ok {
					flush()
					return
				}
				record := r.jobLogRecord(ctx, jobID, attempts, workflow, log)
				if record == nil {
					continue
				}
				records = append(records, record)
				if len(records) >= batchSize {
					flush()
				}
			case <-ticker.C:
				flush()
			case <-done:
				flush()
				return
			}
		}
	}()

	return logErrors
}

func (r *Repository) jobLogRecord(
	ctx context.Context,
	jobID string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
	log *jobsmodel.JobLog,
) *kgo.Record {
	event := &jobsmodel.JobLogEvent{
		EventKey:    idempotency.LogEventKey(jobID, log.Stream, log.SequenceNum, attempts),
		JobID:       jobID,
		WorkflowID:  workflow.GetId(),
		UserID:      workflow.GetUserId(),
		Message:     log.Message,
		TimeStamp:   log.Timestamp,
		SequenceNum: log.SequenceNum,
		Stream:      log.Stream,
		Retention:   workflow.GetLogRetention(),
	}

	r.publishLiveJobLog(ctx, event)

	record, err := joblogevents.KafkaRecord(event)
	if err != nil {
		loggerpkg.FromContext(ctx).Error("failed to create job log kafka record",
			zap.String("job_id", event.JobID),
			zap.String("workflow_id", event.WorkflowID),
			zap.String("event_key", event.EventKey),
			zap.String("stream", event.Stream),
			zap.Uint32("sequence_num", event.SequenceNum),
			zap.Error(err),
		)
		return nil
	}

	return record
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
