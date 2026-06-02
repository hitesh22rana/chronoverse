package workflow

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/twmb/franz-go/pkg/kgo"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	retrypkg "github.com/hitesh22rana/chronoverse/internal/pkg/retry"
)

const (
	buildWorkflowDefaultExpirationTimeout = 5 * time.Minute
)

// buildWorkflow executes the build workflow.
//
//nolint:gocyclo // Ignore the cyclomatic complexity as it is required for the workflow execution
func (r *Repository) buildWorkflow(parentCtx context.Context, workflowEvent *workflowsmodel.WorkflowEvent) error {
	workflowID := workflowEvent.ID
	occurrenceKey := workflowOccurrenceKey(workflowEvent)
	scheduleIdempotencyKey := idempotency.AutomaticScheduleEventKey(occurrenceKey)

	// Issue necessary headers and tokens for authorization
	ctx, err := r.withAuthorization(parentCtx)
	if err != nil {
		return err
	}

	// This context is used for sending notifications, as we don't want to propagate the cancellation
	// This context does not use the parent context
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	notificationCtx, _ := r.withAuthorization(context.Background())

	// Get the workflow details
	workflow, err := r.svc.Workflows.GetWorkflowByID(ctx, &workflowspb.GetWorkflowByIDRequest{
		Id: workflowID,
	})
	if err != nil {
		return err
	}

	if isStaleWorkflowEvent(workflow, workflowEvent) {
		return nil
	}

	// Ensure the workflow is not already terminated
	if workflow.GetTerminatedAt() != "" {
		return status.Error(codes.FailedPrecondition, "workflow is already terminated")
	}

	// If the build already completed, retry the automatic scheduling side effect
	// idempotently in case the previous delivery crashed after updating status.
	if workflow.GetBuildStatus() == workflowsmodel.WorkflowBuildStatusCompleted.ToString() {
		_, scheduleErr := r.svc.Jobs.ScheduleJob(ctx, &jobspb.ScheduleJobRequest{
			WorkflowId:         workflowID,
			UserId:             workflow.GetUserId(),
			ScheduledAt:        time.Now().Add(time.Minute * time.Duration(workflow.GetInterval())).Format(time.RFC3339Nano),
			Trigger:            jobsmodel.JobTriggerAutomatic.ToString(),
			IdempotencyKey:     scheduleIdempotencyKey,
			WorkflowGeneration: workflowEvent.Generation,
		})
		if status.Code(scheduleErr) == codes.FailedPrecondition {
			return nil
		}
		return scheduleErr
	}

	buildStatus := workflow.GetBuildStatus()
	if !isResumableWorkflowBuildStatus(buildStatus) {
		return nil
	}

	// Acquire a distributed lock to ensure only one worker processes the job at a time
	lockKey := fmt.Sprintf(
		"%s:%s:%s",
		lockKeyPrefix,
		workflowsmodel.ActionBuild.ToString(),
		workflowID,
	)
	isLockAcquired, err := r.rdb.AcquireDistributedLock(parentCtx, lockKey, buildWorkflowDefaultExpirationTimeout)
	if err != nil || !isLockAcquired {
		return status.Error(codes.Aborted, "failed to acquire distributed lock")
	}

	// Release the distributed lock
	defer func() {
		//nolint:errcheck // Ignore the error as we don't want to block the job execution, since, the lock might have been auto-released due to expiration
		_ = r.rdb.ReleaseDistributedLock(parentCtx, lockKey)
	}()

	// Cancel all the jobs which are in the QUEUED or PENDING state
	// This handles the condition where the worklow is updated, since the interval might be changed
	//nolint:govet // Ignore shadow of error variable
	if err := r.cancelJobsWithStatus(ctx, workflow, workflow.GetUserId(), jobsmodel.JobStatusQueued.ToString()); err != nil {
		return err
	}

	//nolint:govet // Ignore shadow of error variable
	if err := r.cancelJobsWithStatus(ctx, workflow, workflow.GetUserId(), jobsmodel.JobStatusPending.ToString()); err != nil {
		return err
	}

	analyticEventBytes, err := analyticsmodel.NewAnalyticEventBytesWithKey(
		idempotency.WorkflowAnalyticsEventKey(workflowID),
		workflow.GetUserId(),
		workflowID,
		analyticsmodel.EventTypeWorkflows,
		&analyticsmodel.EventTypeWorkflowsData{
			Kind: workflow.GetKind(),
		},
	)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal analytic event: %v", err)
	}

	record := &kgo.Record{
		Topic: kafka.TopicAnalytics,
		Key:   []byte(workflowID),
		Value: analyticEventBytes,
	}
	if err = r.kfk.ProduceSync(ctx, record).FirstErr(); err != nil {
		return status.Errorf(codes.Unavailable, "failed to publish workflow analytics event: %v", err)
	}

	// If the build step is not required, skip the build process
	if !isBuildStepRequired(workflow.GetKind()) {
		scheduledWorkflow, scheduled, _err := r.completeWorkflowBuildAndSchedule(
			ctx,
			workflowID,
			workflow.GetUserId(),
			workflowEvent.Generation,
			scheduleIdempotencyKey,
			workflowEvent,
		)
		if _err != nil {
			return _err
		}
		if !scheduled {
			return nil
		}

		// Send notification for the workflow build skipped event
		// This is a fire-and-forget operation, so we don't need to wait for it to complete
		//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the workflow execution
		go r.sendNotification(
			notificationCtx,
			scheduledWorkflow.GetUserId(),
			workflowID,
			"",
			"Workflow Build Skipped",
			fmt.Sprintf("Build process for workflow '%s' is skipped and is scheduled to run.", scheduledWorkflow.GetName()),
			notificationsmodel.KindWebInfo.ToString(),
			notificationsmodel.EntityWorkflow.ToString(),
			occurrenceKey,
		)

		return nil
	}

	var (
		updated bool
		_err    error
	)
	if buildStatus == workflowsmodel.WorkflowBuildStatusQueued.ToString() {
		// Update the workflow status from QUEUED to STARTED
		updated, _err = r.updateWorkflowBuildStatus(
			ctx,
			workflowID,
			workflow.GetUserId(),
			workflowsmodel.WorkflowBuildStatusStarted.ToString(),
			workflowEvent.Generation,
		)
		if _err != nil {
			return _err
		}
		if !updated {
			return nil
		}
	}

	// Send notification for the workflow build start event
	// This is a fire-and-forget operation, so we don't need to wait for it to complete
	//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the workflow execution
	go r.sendNotification(
		notificationCtx,
		workflow.GetUserId(),
		workflowID,
		"",
		"Workflow Build Started",
		fmt.Sprintf("Build process for workflow '%s' has started.", workflow.GetName()),
		notificationsmodel.KindWebInfo.ToString(),
		notificationsmodel.EntityWorkflow.ToString(),
		occurrenceKey,
	)

	// Execute the build process with retry enabled
	workflowErr := retrypkg.Do(ctx, 2, retryBackoff, func() error {
		details, err := container.ExtractAndValidateContainerDetails(workflow.GetPayload())
		if err != nil {
			return err
		}

		return r.svc.Csvc.Build(ctx, details.Image)
	})

	// Since, build process can take time to execute and can led to authorization issues
	// So, we need to re-issue the authorization token
	// This context is used for all the gRPC calls
	// This context uses the parent context
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	ctx, _ = r.withAuthorization(ctx)

	// This context is used for sending notifications, as we don't want to propagate the cancellation
	// This context does not use the parent context
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	notificationCtx, _ = r.withAuthorization(context.Background())

	if workflowErr != nil {
		if isTransientBuildRetryError(workflowErr) {
			return workflowErr
		}

		// Update the workflow status from QUEUED to FAILED
		updated, _err = r.updateWorkflowBuildStatus(
			ctx,
			workflowID,
			workflow.GetUserId(),
			workflowsmodel.WorkflowBuildStatusFailed.ToString(),
			workflowEvent.Generation,
		)
		if _err != nil {
			return _err
		}
		if !updated {
			return nil
		}

		// Send notification for the workflow build failed event
		// This is a fire-and-forget operation, so we don't need to wait for it to complete
		//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the workflow execution
		go r.sendNotification(
			notificationCtx,
			workflow.GetUserId(),
			workflowID,
			"",
			"Workflow Build Failed",
			fmt.Sprintf("Build process for workflow '%s' has failed.", workflow.GetName()),
			notificationsmodel.KindWebAlert.ToString(),
			notificationsmodel.EntityWorkflow.ToString(),
			occurrenceKey,
		)

		return workflowErr
	}

	scheduledWorkflow, scheduled, _err := r.completeWorkflowBuildAndSchedule(
		ctx,
		workflowID,
		workflow.GetUserId(),
		workflowEvent.Generation,
		scheduleIdempotencyKey,
		workflowEvent,
	)
	if _err != nil {
		return _err
	}
	if !scheduled {
		return nil
	}

	// Send notification for the workflow build completed event
	// This is a fire-and-forget operation, so we don't need to wait for it to complete
	//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the workflow build process
	go r.sendNotification(
		notificationCtx,
		scheduledWorkflow.GetUserId(),
		workflowID,
		"",
		"Workflow Build Completed",
		fmt.Sprintf("Build process for workflow '%s' has completed and is scheduled to run.", scheduledWorkflow.GetName()),
		notificationsmodel.KindWebSuccess.ToString(),
		notificationsmodel.EntityWorkflow.ToString(),
		occurrenceKey,
	)

	return nil
}

// isBuildStepRequired checks if the build step is required for the given kind.
func isBuildStepRequired(kind string) bool {
	switch kind {
	case workflowsmodel.KindHeartbeat.ToString():
		return false
	default:
		return true
	}
}

func isResumableWorkflowBuildStatus(buildStatus string) bool {
	return buildStatus == workflowsmodel.WorkflowBuildStatusQueued.ToString() ||
		buildStatus == workflowsmodel.WorkflowBuildStatusStarted.ToString()
}

func isTransientBuildRetryError(err error) bool {
	return status.Code(err) == codes.ResourceExhausted
}

func (r *Repository) updateWorkflowBuildStatus(ctx context.Context, workflowID, userID, buildStatus string, generation int64) (bool, error) {
	_, err := r.svc.Workflows.UpdateWorkflowBuildStatus(ctx, &workflowspb.UpdateWorkflowBuildStatusRequest{
		Id:          workflowID,
		UserId:      userID,
		BuildStatus: buildStatus,
		Generation:  generation,
	})
	if err != nil {
		if status.Code(err) == codes.FailedPrecondition {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func (r *Repository) completeWorkflowBuildAndSchedule(
	ctx context.Context,
	workflowID, userID string,
	generation int64,
	scheduleIdempotencyKey string,
	workflowEvent *workflowsmodel.WorkflowEvent,
) (*workflowspb.GetWorkflowByIDResponse, bool, error) {
	updated, err := r.updateWorkflowBuildStatus(
		ctx,
		workflowID,
		userID,
		workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
		generation,
	)
	if err != nil || !updated {
		return nil, false, err
	}

	workflow, schedulable, err := r.getSchedulableWorkflow(ctx, workflowID, workflowEvent)
	if err != nil || !schedulable {
		return nil, false, err
	}

	if err := r.scheduleAutomaticJob(ctx, workflow, scheduleIdempotencyKey); err != nil {
		return nil, false, err
	}

	return workflow, true, nil
}

func (r *Repository) getSchedulableWorkflow(ctx context.Context, workflowID string, workflowEvent *workflowsmodel.WorkflowEvent) (*workflowspb.GetWorkflowByIDResponse, bool, error) {
	workflow, err := r.svc.Workflows.GetWorkflowByID(ctx, &workflowspb.GetWorkflowByIDRequest{
		Id: workflowID,
	})
	if err != nil {
		return nil, false, err
	}

	if isStaleWorkflowEvent(workflow, workflowEvent) || workflow.GetTerminatedAt() != "" {
		return nil, false, nil
	}

	return workflow, true, nil
}

func (r *Repository) scheduleAutomaticJob(ctx context.Context, workflow *workflowspb.GetWorkflowByIDResponse, idempotencyKey string) error {
	_, err := r.svc.Jobs.ScheduleJob(ctx, &jobspb.ScheduleJobRequest{
		WorkflowId:         workflow.GetId(),
		UserId:             workflow.GetUserId(),
		ScheduledAt:        time.Now().Add(time.Minute * time.Duration(workflow.GetInterval())).Format(time.RFC3339Nano),
		Trigger:            jobsmodel.JobTriggerAutomatic.ToString(),
		IdempotencyKey:     idempotencyKey,
		WorkflowGeneration: workflow.GetGeneration(),
	})
	if status.Code(err) == codes.FailedPrecondition {
		return nil
	}

	return err
}
