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

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
)

const (
	containerWorkflowDefaultExecutionTimeout = 10 * time.Second
	bufferPercentageTimeout                  = 10
)

var runningStatus = jobsmodel.JobStatusRunning.ToString()

// cancelJobs cancels the jobs of the workflow.
// This function is invoked via the cancelJobsWithStatus function.
func (r *Repository) cancelJobs(ctx context.Context, userID, workflowID string, jobs *jobspb.ListJobsResponse) {
	// This context is used for sending notifications, as we don't want to propagate the cancellation
	// This context does not use the parent context
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	notificationCtx, _ := r.withAuthorization(context.Background())

	// Iterate over the jobs and cancel them
	for _, job := range jobs.GetJobs() {
		//nolint:errcheck // Ignore the error as we don't want to block the job execution
		r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
			Id:     job.GetId(),
			Status: jobsmodel.JobStatusCanceled.ToString(),
		})

		// Send notification for the job termination
		// This is a fire-and-forget operation, so we don't need to wait for it to complete
		//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the job execution
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

		r.cancelJobs(ctx, userID, workflowID, jobs)

		if jobs.GetCursor() == "" {
			break
		}

		cursor = jobs.GetCursor()
	}

	return nil
}

// extractWorkflowTimeout extracts the timeout from the workflow payload.
func extractWorkflowTimeout(workflowPayload string) (time.Duration, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(workflowPayload), &payload); err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "invalid workflow payload format: %v", err)
	}

	timeoutStr, ok := payload["timeout"].(string)
	if !ok {
		return containerWorkflowDefaultExecutionTimeout, nil
	}

	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "invalid timeout value: %v", err)
	}

	if timeout <= 0 {
		return 0, status.Errorf(codes.InvalidArgument, "timeout must be greater than zero")
	}

	return timeout, nil
}

// cancelRunningJobs cancels the running jobs of the workflow.
func (r *Repository) cancelRunningJobs(ctx context.Context, workflowID, userID, workflowPayload string) error {
	// Extract the timeout from the workflow payload
	workflowTimeOut, err := extractWorkflowTimeout(workflowPayload)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to extract workflow timeout: %v", err)
	}

	// Add a buffer to the timeout to account for any delays
	// This is to ensure that we don't cancel jobs that are still running but close to the timeout
	workflowTimeOut += workflowTimeOut / bufferPercentageTimeout

	// Get all the jobs of the workflow which are in the RUNNING state
	cursor := ""
	for {
		// Issue necessary headers and tokens for authorization
		// This context uses the parent context
		//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
		ctx, _ = r.withAuthorization(ctx)

		jobs, err := r.svc.Jobs.ListJobs(ctx, &jobspb.ListJobsRequest{
			WorkflowId: workflowID,
			UserId:     userID,
			Cursor:     cursor,
			Status:     &runningStatus,
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
		//nolint:errcheck // Ignore the error as we don't want to block the job execution
		for _, job := range jobs.GetJobs() {
			if job.GetStartedAt() == "" {
				jobsToCancel = append(jobsToCancel, job)
				continue
			}

			startTime, _ := time.Parse(time.RFC3339Nano, job.GetStartedAt())
			if time.Since(startTime) > workflowTimeOut {
				jobsToCancel = append(jobsToCancel, job)
			}
		}

		r.cancelJobs(ctx, userID, workflowID, &jobspb.ListJobsResponse{
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
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	r.cancelJobsWithStatus(ctx, workflowID, userID, jobsmodel.JobStatusQueued.ToString())
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	r.cancelJobsWithStatus(ctx, workflowID, userID, jobsmodel.JobStatusPending.ToString())

	// Cancel all the hanging jobs which are in the RUNNING state
	// This is to ensure that we don't leave any jobs running after the workflow is terminated
	//nolint:errcheck // Ignore the error as we don't want to block the workflow build process
	r.cancelRunningJobs(ctx, workflowID, userID, workflow.GetPayload())

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
