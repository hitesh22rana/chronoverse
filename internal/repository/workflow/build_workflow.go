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
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

const (
	buildWorkflowDefaultExpirationTimeout = 5 * time.Minute
)

// buildWorkflow executes the build workflow.
//
//nolint:gocyclo // Ignore the cyclomatic complexity as it is required for the workflow execution
func (r *Repository) buildWorkflow(parentCtx context.Context, workflowID string) error {
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

	// Early return idempotency checks
	// Ensure the build process is not already started
	if workflow.GetBuildStatus() != workflowsmodel.WorkflowBuildStatusQueued.ToString() {
		return nil
	}

	// Ensure the workflow is not already terminated
	if workflow.GetTerminatedAt() != "" {
		return status.Error(codes.FailedPrecondition, "workflow is already terminated")
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

	analyticEventBytes, err := analyticsmodel.NewAnalyticEventBytes(
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

	// Asynchronously produce the record to Kafka
	// This is a fire-and-forget operation, so we don't need to wait for it to complete
	// This is used to track the number of jobs executed for the workflow
	r.kfk.Produce(context.WithoutCancel(ctx), record, func(_ *kgo.Record, _ error) {})

	// If the build step is not required, skip the build process
	if !isBuildStepRequired(workflow.GetKind()) {
		// Update the workflow status from QUEUED to COMPLETED
		if _, _err := r.svc.Workflows.UpdateWorkflowBuildStatus(ctx, &workflowspb.UpdateWorkflowBuildStatusRequest{
			Id:          workflowID,
			UserId:      workflow.GetUserId(),
			BuildStatus: workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
		}); _err != nil {
			return _err
		}

		// Schedule the workflow for the run
		if _, _err := r.svc.Jobs.ScheduleJob(ctx, &jobspb.ScheduleJobRequest{
			WorkflowId:  workflowID,
			UserId:      workflow.GetUserId(),
			ScheduledAt: time.Now().Add(time.Minute * time.Duration(workflow.GetInterval())).Format(time.RFC3339Nano),
		}); _err != nil {
			return _err
		}

		// Send notification for the workflow build skipped event
		// This is a fire-and-forget operation, so we don't need to wait for it to complete
		//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the workflow execution
		go r.sendNotification(
			notificationCtx,
			workflow.GetUserId(),
			workflowID,
			"",
			"Workflow Build Skipped",
			fmt.Sprintf("Build process for workflow '%s' is skipped and is scheduled to run.", workflow.GetName()),
			notificationsmodel.KindWebInfo.ToString(),
			notificationsmodel.EntityWorkflow.ToString(),
		)

		return nil
	}

	// Update the workflow status from QUEUED to STARTED
	if _, _err := r.svc.Workflows.UpdateWorkflowBuildStatus(ctx, &workflowspb.UpdateWorkflowBuildStatusRequest{
		Id:          workflowID,
		UserId:      workflow.GetUserId(),
		BuildStatus: workflowsmodel.WorkflowBuildStatusStarted.ToString(),
	}); _err != nil {
		return _err
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
	)

	// Execute the build process with retry enabled
	workflowErr := withRetry(func() error {
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
		// Update the workflow status from QUEUED to FAILED
		if _, _err := r.svc.Workflows.UpdateWorkflowBuildStatus(ctx, &workflowspb.UpdateWorkflowBuildStatusRequest{
			Id:          workflowID,
			UserId:      workflow.GetUserId(),
			BuildStatus: workflowsmodel.WorkflowBuildStatusFailed.ToString(),
		}); _err != nil {
			return _err
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
		)

		return workflowErr
	}

	// Update the workflow status from QUEUED to COMPLETED
	if _, _err := r.svc.Workflows.UpdateWorkflowBuildStatus(ctx, &workflowspb.UpdateWorkflowBuildStatusRequest{
		Id:          workflowID,
		UserId:      workflow.GetUserId(),
		BuildStatus: workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
	}); _err != nil {
		return _err
	}

	// Schedule the workflow for the run
	if _, _err := r.svc.Jobs.ScheduleJob(ctx, &jobspb.ScheduleJobRequest{
		WorkflowId:  workflowID,
		UserId:      workflow.GetUserId(),
		ScheduledAt: time.Now().Add(time.Minute * time.Duration(workflow.GetInterval())).Format(time.RFC3339Nano),
	}); _err != nil {
		return _err
	}

	// Send notification for the workflow build completed event
	// This is a fire-and-forget operation, so we don't need to wait for it to complete
	//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the workflow build process
	go r.sendNotification(
		notificationCtx,
		workflow.GetUserId(),
		workflowID,
		"",
		"Workflow Build Completed",
		fmt.Sprintf("Build process for workflow '%s' has completed and is scheduled to run.", workflow.GetName()),
		notificationsmodel.KindWebSuccess.ToString(),
		notificationsmodel.EntityWorkflow.ToString(),
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
