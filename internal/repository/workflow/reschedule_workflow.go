package workflow

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
)

const rescheduleWorkflowExpirationTimeout = 5 * time.Minute

func (r *Repository) rescheduleWorkflow(parentCtx context.Context, workflowEvent workflowsmodel.WorkflowEvent) error {
	workflowID := workflowEvent.ID
	userID := workflowEvent.UserID

	ctx, err := r.withAuthorization(parentCtx)
	if err != nil {
		return err
	}

	workflow, err := r.svc.Workflows.GetWorkflowByID(ctx, &workflowspb.GetWorkflowByIDRequest{Id: workflowID})
	if err != nil {
		return err
	}

	if isStaleWorkflowEvent(workflow, workflowEvent) {
		return nil
	}

	if workflow.GetTerminatedAt() != "" {
		return status.Error(codes.FailedPrecondition, "workflow is already terminated")
	}
	if workflow.GetBuildStatus() != workflowsmodel.WorkflowBuildStatusCompleted.ToString() {
		return nil
	}

	lockKey := fmt.Sprintf("%s:%s:%s", lockKeyPrefix, workflowsmodel.ActionReschedule.ToString(), workflowID)
	isLockAcquired, err := r.rdb.AcquireDistributedLock(parentCtx, lockKey, rescheduleWorkflowExpirationTimeout)
	if err != nil || !isLockAcquired {
		return status.Error(codes.Aborted, "failed to acquire distributed lock")
	}
	defer func() {
		//nolint:errcheck // Lock may have expired.
		_ = r.rdb.ReleaseDistributedLock(parentCtx, lockKey)
	}()

	replacementJobID, err := r.svc.Jobs.ScheduleJob(ctx, &jobspb.ScheduleJobRequest{
		WorkflowId:     workflowID,
		UserId:         userID,
		ScheduledAt:    time.Now().Add(time.Minute * time.Duration(workflow.GetInterval())).Format(time.RFC3339Nano),
		Trigger:        jobsmodel.JobTriggerAutomatic.ToString(),
		IdempotencyKey: idempotency.AutomaticScheduleEventKey(workflowOccurrenceKey(workflowEvent)),
	})
	if err != nil {
		return err
	}

	if cancelErr := r.cancelJobsWithStatusExcept(ctx, workflow, userID, jobsmodel.JobStatusPending.ToString(), replacementJobID.GetId()); cancelErr != nil {
		return cancelErr
	}

	return r.cancelJobsWithStatusExcept(ctx, workflow, userID, jobsmodel.JobStatusQueued.ToString(), replacementJobID.GetId())
}
