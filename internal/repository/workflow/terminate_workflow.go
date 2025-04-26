package workflow

import (
	"context"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
)

const (
	// job statuses.
	jobStatusQueued   = "QUEUED"
	jobStatusPending  = "PENDING"
	jobStatusCanceled = "CANCELED"
)

func (r *Repository) terminate(ctx, notificationCtx context.Context, workflowID, userID string, jobs *jobspb.ListJobsResponse) error {
	// Iterate over the jobs and terminate them
	for _, job := range jobs.GetJobs() {
		// Terminate the job
		if _, err := r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
			Id:     job.GetId(),
			Status: jobStatusCanceled,
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
			"Job Terminated",
			"Job has been terminated",
			notificationsmodel.KindWebInfo.ToString(),
			notificationsmodel.EntityJob.ToString(),
		)
	}
	return nil
}

// terminate workflow terminates the workflow.
func (r *Repository) terminateWorkflow(ctx context.Context, workflowID, userID string) error {
	// Get all the jobs of the workflow which are in the QUEUED state
	cursor := ""
	queuedStatus := jobStatusQueued
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
			Status:     &queuedStatus,
		})
		if err != nil {
			return err
		}

		if len(jobs.GetJobs()) == 0 {
			break
		}

		// Terminate the jobs
		if err := r.terminate(ctx, notificationCtx, userID, workflowID, jobs); err != nil {
			return err
		}

		// Check if there are more jobs to process
		if jobs.GetCursor() == "" {
			break
		}

		cursor = jobs.GetCursor()
	}

	// Get all the jobs of the workflow which are in the PENDING state
	cursor = ""
	pendingStatus := jobStatusPending

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
			Status:     &pendingStatus,
		})
		if err != nil {
			return err
		}

		if len(jobs.GetJobs()) == 0 {
			break
		}

		// Terminate the jobs
		if err := r.terminate(ctx, notificationCtx, userID, workflowID, jobs); err != nil {
			return err
		}

		// Check if there are more jobs to process
		if jobs.GetCursor() == "" {
			break
		}

		cursor = jobs.GetCursor()
	}

	return nil
}
