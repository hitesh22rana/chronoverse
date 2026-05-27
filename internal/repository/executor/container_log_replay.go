package executor

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func (r *Repository) replayContainerLogs(
	parentCtx context.Context,
	claim *jobspb.ClaimJobResponse,
	workflow *workflowspb.GetWorkflowByIDResponse,
	containerID string,
) error {
	if containerID == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(parentCtx, containerLogReplayTimeout)
	defer cancel()

	logs, errs, err := r.svc.Csvc.Logs(ctx, containerID)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil
		}
		return err
	}

	for logs != nil || errs != nil {
		select {
		case log, ok := <-logs:
			if !ok {
				logs = nil
				continue
			}
			if err := r.enqueueRecoveredContainerLog(ctx, claim, workflow, log); err != nil {
				return err
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if status.Code(err) == codes.NotFound {
				return nil
			}
			return err
		case <-ctx.Done():
			return contextError(ctx.Err())
		}
	}

	return nil
}

func (r *Repository) enqueueRecoveredContainerLog(
	ctx context.Context,
	claim *jobspb.ClaimJobResponse,
	workflow *workflowspb.GetWorkflowByIDResponse,
	log *jobsmodel.JobLog,
) error {
	if log == nil {
		return nil
	}

	workflowID := claim.GetWorkflowId()
	userID := claim.GetUserId()
	retention := false
	if workflow != nil {
		workflowID = workflow.GetId()
		userID = workflow.GetUserId()
		retention = workflow.GetLogRetention()
	}

	return r.enqueueJobLogEvent(ctx, &jobsmodel.JobLogEvent{
		EventKey:    idempotency.LogEventKey(claim.GetId(), log.Stream, log.SequenceNum, claim.GetAttempts()),
		JobID:       claim.GetId(),
		WorkflowID:  workflowID,
		UserID:      userID,
		Message:     log.Message,
		TimeStamp:   recoveredLogTimestamp(log),
		SequenceNum: log.SequenceNum,
		Stream:      log.Stream,
		Retention:   retention,
	})
}

func recoveredLogTimestamp(log *jobsmodel.JobLog) time.Time {
	if log == nil || log.Timestamp.IsZero() {
		return time.Now().UTC()
	}

	return log.Timestamp
}
