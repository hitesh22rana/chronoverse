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
	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
)

const (
	terminateWorkflowExpirationTimeout = 5 * time.Minute
)

// cancelJobs cancels the jobs of the workflow.
// This function is invoked via the cancelJobsWithStatus function.
func (r *Repository) cancelJobs(parentCtx context.Context, workflow *workflowspb.GetWorkflowByIDResponse, userID string, jobs *jobspb.ListJobsResponse) {
	bgCtx := context.Background()
	// Iterate over the jobs and cancel them
	for _, job := range jobs.GetJobs() {
		switch workflow.GetKind() {
		// If the job is a container job, we need to stop the running container
		case workflowsmodel.KindContainer.ToString():
			//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the job execution
			r.svc.Csvc.Terminate(bgCtx, job.GetContainerId())
		default:
		}

		// Issue necessary headers and tokens for authorization
		// This context uses the parent context
		//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
		ctx, _ := r.withAuthorization(parentCtx)

		//nolint:errcheck // Ignore the error as we don't want to block the job execution
		r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
			Id:     job.GetId(),
			Status: jobsmodel.JobStatusCanceled.ToString(),
		})

		// This context is used for sending notifications, as we don't want to propagate the cancellation
		// This context does not use the parent context
		//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
		notificationCtx, _ := r.withAuthorization(bgCtx)

		// Send notification for the job termination
		// This is a fire-and-forget operation, so we don't need to wait for it to complete
		//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the job execution
		go r.sendNotification(
			notificationCtx,
			userID,
			workflow.GetId(),
			job.GetId(),
			"Job Canceled",
			"Job has been canceled",
			notificationsmodel.KindWebInfo.ToString(),
			notificationsmodel.EntityJob.ToString(),
		)
	}
}

// cancelJobs cancels the jobs of the workflow with the specified status.
func (r *Repository) cancelJobsWithStatus(parentCtx context.Context, workflow *workflowspb.GetWorkflowByIDResponse, userID, status string) error {
	// Get all the jobs of the workflow which are in the specified status
	cursor := ""
	for {
		// Issue necessary headers and tokens for authorization
		// This context uses the parent context
		//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
		ctx, _ := r.withAuthorization(parentCtx)

		jobs, err := r.svc.Jobs.ListJobs(ctx, &jobspb.ListJobsRequest{
			WorkflowId: workflow.GetId(),
			UserId:     userID,
			Cursor:     cursor,
			Filters: &jobspb.ListJobsFilters{
				Status: status,
			},
		})
		if err != nil {
			return err
		}

		if len(jobs.GetJobs()) == 0 {
			break
		}

		r.cancelJobs(ctx, workflow, userID, jobs)

		if jobs.GetCursor() == "" {
			break
		}

		cursor = jobs.GetCursor()
	}

	return nil
}

// cancelRunningJobs cancels the running jobs of the workflow.
func (r *Repository) cancelRunningJobs(parentCtx context.Context, workflow *workflowspb.GetWorkflowByIDResponse, userID string) error {
	// Get all the jobs of the workflow which are in the RUNNING state
	cursor := ""
	for {
		// Issue necessary headers and tokens for authorization
		// This context uses the parent context
		//nolint:errcheck // Ignore the error as we don't want to block the job termination
		ctx, _ := r.withAuthorization(parentCtx)

		jobs, err := r.svc.Jobs.ListJobs(ctx, &jobspb.ListJobsRequest{
			WorkflowId: workflow.GetId(),
			UserId:     userID,
			Cursor:     cursor,
			Filters: &jobspb.ListJobsFilters{
				Status: jobsmodel.JobStatusRunning.ToString(),
			},
		})
		if err != nil {
			return err
		}

		if len(jobs.GetJobs()) == 0 {
			break
		}

		// Cancel all running jobs which match the following criteria:
		// 1. StartedAt is not set (i.e., the job has not started yet)
		// 2. Time since, the job has been started is greater than the configured timeout for the workflow
		jobsToCancel := make([]*jobspb.JobsResponse, 0, len(jobs.GetJobs()))

		// Iterate over the jobs and filter the ones to cancel
		for _, job := range jobs.GetJobs() {
			if job.GetStartedAt() == "" {
				jobsToCancel = append(jobsToCancel, job)
				continue
			}

			// Skip the job if the started time is not valid
			if _, parseError := time.Parse(time.RFC3339Nano, job.GetStartedAt()); parseError != nil {
				continue
			}

			jobsToCancel = append(jobsToCancel, job)
		}

		r.cancelJobs(ctx, workflow, userID, &jobspb.ListJobsResponse{
			Jobs:   jobsToCancel,
			Cursor: jobs.GetCursor(),
		})

		if jobs.GetCursor() == "" {
			break
		}

		cursor = jobs.GetCursor()
	}

	return nil
}

// terminate workflow terminates the workflow.
func (r *Repository) terminateWorkflow(parentCtx context.Context, workflowID, userID string) error {
	// Issue necessary headers and tokens for authorization
	ctx, err := r.withAuthorization(parentCtx)
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

	// Acquire a distributed lock to ensure only one worker processes the job at a time
	lockKey := fmt.Sprintf(
		"%s:%s:%s",
		lockKeyPrefix,
		workflowsmodel.ActionTerminate.ToString(),
		workflowID,
	)
	isLockAcquired, err := r.rdb.AcquireDistributedLock(parentCtx, lockKey, terminateWorkflowExpirationTimeout)
	if err != nil || !isLockAcquired {
		return status.Error(codes.Aborted, "failed to acquire distributed lock")
	}

	// Release the distributed lock
	defer func() {
		//nolint:errcheck // Ignore the error as we don't want to block the job execution, since, the lock might have been auto-released due to expiration
		_ = r.rdb.ReleaseDistributedLock(parentCtx, lockKey)
	}()

	// Cancel all the jobs which are in the QUEUED or PENDING state
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	r.cancelJobsWithStatus(ctx, workflow, userID, jobsmodel.JobStatusPending.ToString())
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	r.cancelJobsWithStatus(ctx, workflow, userID, jobsmodel.JobStatusQueued.ToString())

	// Cancel all the hanging jobs which are in the RUNNING state
	// This is to ensure that we don't leave any jobs running after the workflow is terminated
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	r.cancelRunningJobs(ctx, workflow, userID)

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
