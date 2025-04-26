package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
)

const (
	// workflowBuild statuses.
	workflowBuildQueued    = "QUEUED"
	workflowBuildStarted   = "STARTED"
	workflowBuildCompleted = "COMPLETED"
	workflowBuildFailed    = "FAILED"
)

// buildWorkflow executes the build workflow.
func (r *Repository) buildWorkflow(ctx context.Context, workflowID string) error {
	// Issue necessary headers and tokens for authorization
	ctx, err := r.withAuthorization(ctx)
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
	if workflow.GetBuildStatus() != workflowBuildQueued {
		return nil
	}

	// Ensure the workflow is not already terminated
	if workflow.GetTerminatedAt() != "" {
		return status.Error(codes.FailedPrecondition, "workflow is already terminated")
	}

	// If the build step is not required, skip the build process
	if !isBuildStepRequired(workflow.GetKind()) {
		// Update the workflow status from QUEUED to COMPLETED
		if _, _err := r.svc.Workflows.UpdateWorkflowBuildStatus(ctx, &workflowspb.UpdateWorkflowBuildStatusRequest{
			Id:          workflowID,
			UserId:      workflow.GetUserId(),
			BuildStatus: workflowBuildCompleted,
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
		BuildStatus: workflowBuildStarted,
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
		// Extract the image from the workflow
		imageName, err := extractImageName(workflow.GetPayload())
		if err != nil {
			return err
		}

		return r.svc.Csvc.Build(ctx, imageName)
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
			BuildStatus: workflowBuildFailed,
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
			notificationsmodel.KindWebError.ToString(),
			notificationsmodel.EntityWorkflow.ToString(),
		)

		return workflowErr
	}

	// Update the workflow status from QUEUED to COMPLETED
	if _, _err := r.svc.Workflows.UpdateWorkflowBuildStatus(ctx, &workflowspb.UpdateWorkflowBuildStatusRequest{
		Id:          workflowID,
		UserId:      workflow.GetUserId(),
		BuildStatus: workflowBuildCompleted,
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

// extractImageName extracts the image name from the workflow payload.
func extractImageName(payload string) (string, error) {
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return "", status.Error(codes.InvalidArgument, "invalid payload format")
	}

	imageName, ok := data["image"].(string)
	if !ok || imageName == "" {
		return "", status.Error(codes.InvalidArgument, "invalid image name")
	}

	return imageName, nil
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
