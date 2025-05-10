package workflow

import (
	"context"
	"fmt"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
)

// cancelJobs cancels the jobs of the workflow.
// This function is invoked via the cancelJobsWithStatus function
func (r *Repository) cancelJobs(ctx, notificationCtx context.Context, workflowID, userID string, jobs *jobspb.ListJobsResponse) error {
	// Iterate over the jobs and cancel them
	for _, job := range jobs.GetJobs() {
		if _, err := r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
			Id:     job.GetId(),
			Status: jobsmodel.JobStatusCanceled.ToString(),
		}); err != nil {
			return err
		}

		// Send notification for the job termination
		// This is a fire-and-forget operation, so we don't need to wait for it to complete
		//nolint:errcheck // Ignore the error as we don't want to block the job execution
		go r.sendNotification(
			notificationCtx,
			userID,
			workflowID,
			job.GetId(),
			"Job Canceled",
			"Job has been canceled",
			notificationsmodel.KindWebInfo.ToString(),
			notificationsmodel.EntityJob.ToString(),
		)
	}
	return nil
}

// cancelJobs cancels the jobs of the workflow with the specified status.
func (r *Repository) cancelJobsWithStatus(ctx context.Context, workflowID, userID, status string) error {
	// Get all the jobs of the workflow which are in the specified status
	cursor := ""
	for {
		// Issue necessary headers and tokens for authorization
		// This context uses the parent context
		//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
		ctx, _ = r.withAuthorization(ctx)

		// This context is used for sending notifications, as we don't want to propagate the cancellation
		// This context does not use the parent context
		//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
		notificationCtx, _ := r.withAuthorization(context.Background())

		jobs, err := r.svc.Jobs.ListJobs(ctx, &jobspb.ListJobsRequest{
			WorkflowId: workflowID,
			UserId:     userID,
			Cursor:     cursor,
			Status:     &status,
		})
		if err != nil {
			return err
		}

		if len(jobs.GetJobs()) == 0 {
			break
		}

		if err := r.cancelJobs(ctx, notificationCtx, userID, workflowID, jobs); err != nil {
			return err
		}

		if jobs.GetCursor() == "" {
			break
		}

		cursor = jobs.GetCursor()
	}

	return nil
}

// terminate workflow terminates the workflow.
func (r *Repository) terminateWorkflow(ctx context.Context, workflowID, userID string) error {
	// Issue necessary headers and tokens for authorization
	ctx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	// Get the workflow details
	workflow, err := r.svc.Workflows.GetWorkflowByID(ctx, &workflowspb.GetWorkflowByIDRequest{
		Id: workflowID,
	})
	if err != nil {
		return err
	}

	// Cancel all the jobs which are in the QUEUED or PENDING state
	if err := r.cancelJobsWithStatus(ctx, workflowID, userID, jobsmodel.JobStatusQueued.ToString()); err != nil {
		return err
	}
	if err := r.cancelJobsWithStatus(ctx, workflowID, userID, jobsmodel.JobStatusPending.ToString()); err != nil {
		return err
	}

	// This context is used for sending notifications, as we don't want to propagate the cancellation
	// This context does not use the parent context
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	notificationsCtx, _ := r.withAuthorization(context.Background())

	// Send notification for the workflow termination
	// This is a fire-and-forget operation, so we don't need to wait for it to complete
	//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the workflow execution
	go r.sendNotification(
		notificationsCtx,
		userID,
		workflowID,
		"",
		"Workflow Terminated",
		fmt.Sprintf("Workflow '%s' has been terminated.", workflow.GetName()),
		notificationsmodel.KindWebInfo.ToString(),
		notificationsmodel.EntityWorkflow.ToString(),
	)

	return nil
}
