package workflow

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

const (
	terminateWorkflowExpirationTimeout = 5 * time.Minute
	canceledJobLogReplayTimeout        = 2 * time.Minute
)

// cancelJobs marks the workflow jobs as canceled and returns the jobs that need container cleanup.
// This function is invoked via the cancelJobsWithStatus function.
func (r *Repository) cancelJobs(
	parentCtx context.Context,
	workflow *workflowspb.GetWorkflowByIDResponse,
	userID string,
	jobs *jobspb.ListJobsResponse,
	excludedJobIDs map[string]struct{},
) ([]*jobspb.JobsResponse, error) {
	cleanupJobs := make([]*jobspb.JobsResponse, 0, len(jobs.GetJobs()))

	// Iterate over the jobs and cancel them
	for _, job := range jobs.GetJobs() {
		if _, ok := excludedJobIDs[job.GetId()]; ok {
			continue
		}

		canceled, err := r.cancelJobRecord(parentCtx, job.GetId())
		if err != nil {
			return nil, err
		}
		if !canceled {
			continue
		}

		r.sendJobCanceledNotification(parentCtx, userID, workflow, job)
		if shouldCleanupJobContainer(workflow, job) {
			cleanupJobs = append(cleanupJobs, job)
		}
	}

	return cleanupJobs, nil
}

func (r *Repository) cancelJobRecord(parentCtx context.Context, jobID string) (bool, error) {
	// Issue necessary headers and tokens for authorization.
	ctx, err := r.withAuthorization(parentCtx)
	if err != nil {
		return false, err
	}

	if _, err = r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
		Id:     jobID,
		Status: jobsmodel.JobStatusCanceled.ToString(),
	}); err != nil {
		if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.NotFound {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (r *Repository) sendJobCanceledNotification(
	parentCtx context.Context,
	userID string,
	workflow *workflowspb.GetWorkflowByIDResponse,
	job *jobspb.JobsResponse,
) {
	// This context is used for sending notifications, as we don't want to propagate the cancellation.
	notificationCtx, err := r.withAuthorization(context.WithoutCancel(parentCtx))
	if err != nil {
		return
	}

	// Send notification for the job termination.
	// This is a fire-and-forget operation, so we don't need to wait for it to complete.
	//nolint:errcheck // Ignore the error as we don't want to block the job execution.
	go r.sendNotification(
		notificationCtx,
		userID,
		workflow.GetId(),
		job.GetId(),
		"Job Canceled",
		"Job has been canceled",
		notificationsmodel.KindWebInfo.ToString(),
		notificationsmodel.EntityJob.ToString(),
		"",
	)
}

func (r *Repository) sendWorkflowTerminatedNotification(
	parentCtx context.Context,
	userID string,
	workflow *workflowspb.GetWorkflowByIDResponse,
	workflowEvent *workflowsmodel.WorkflowEvent,
) {
	// This context is used for sending notifications, as we don't want to propagate the cancellation.
	notificationCtx, err := r.withAuthorization(context.WithoutCancel(parentCtx))
	if err != nil {
		return
	}

	// Send notification for the workflow termination.
	// This is a fire-and-forget operation, so we don't need to wait for it to complete.
	//nolint:errcheck // Ignore the error as we don't want to block the workflow execution.
	go r.sendNotification(
		notificationCtx,
		userID,
		workflow.GetId(),
		"",
		"Workflow Terminated",
		fmt.Sprintf("Workflow '%s' has been terminated.", workflow.GetName()),
		notificationsmodel.KindWebInfo.ToString(),
		notificationsmodel.EntityWorkflow.ToString(),
		workflowOccurrenceKey(workflowEvent),
	)
}

func shouldCleanupJobContainer(workflow *workflowspb.GetWorkflowByIDResponse, job *jobspb.JobsResponse) bool {
	return workflow.GetKind() == workflowsmodel.KindContainer.ToString() && job.GetContainerId() != ""
}

func (r *Repository) cleanupCanceledJobContainers(
	parentCtx context.Context,
	workflow *workflowspb.GetWorkflowByIDResponse,
	jobs []*jobspb.JobsResponse,
) error {
	var firstErr error
	for _, job := range jobs {
		if err := r.cleanupCanceledJobContainer(parentCtx, workflow, job); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

func (r *Repository) cleanupCanceledJobContainer(parentCtx context.Context, workflow *workflowspb.GetWorkflowByIDResponse, job *jobspb.JobsResponse) error {
	if !shouldCleanupJobContainer(workflow, job) {
		return nil
	}

	cleanupCtx := context.WithoutCancel(parentCtx)
	if err := r.svc.Csvc.Terminate(cleanupCtx, job.GetContainerId()); err != nil {
		return err
	}
	if err := r.replayCanceledJobContainerLogs(parentCtx, workflow, job); err != nil {
		loggerpkg.FromContext(parentCtx).Warn("failed to replay canceled job logs",
			zap.String("workflow_id", workflow.GetId()),
			zap.String("job_id", job.GetId()),
			zap.String("container_id", job.GetContainerId()),
			zap.Error(err),
		)
	}

	return r.svc.Csvc.Remove(cleanupCtx, job.GetContainerId())
}

func (r *Repository) replayCanceledJobContainerLogs(
	parentCtx context.Context,
	workflow *workflowspb.GetWorkflowByIDResponse,
	job *jobspb.JobsResponse,
) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), canceledJobLogReplayTimeout)
	defer cancel()

	logs, errs, err := r.svc.Csvc.Logs(ctx, job.GetContainerId())
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
			if err := r.publishCanceledJobLog(ctx, workflow, job, log); err != nil {
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
			if ctx.Err() == context.DeadlineExceeded {
				return status.Error(codes.DeadlineExceeded, ctx.Err().Error())
			}
			return status.Error(codes.Canceled, ctx.Err().Error())
		}
	}

	return nil
}

func (r *Repository) publishCanceledJobLog(
	ctx context.Context,
	workflow *workflowspb.GetWorkflowByIDResponse,
	job *jobspb.JobsResponse,
	log *jobsmodel.JobLog,
) error {
	if log == nil {
		return nil
	}

	timestamp := log.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	event := &jobsmodel.JobLogEvent{
		EventKey:    idempotency.LogEventKey(job.GetId(), log.Stream, log.SequenceNum, job.GetAttempts()),
		JobID:       job.GetId(),
		WorkflowID:  workflow.GetId(),
		UserID:      workflow.GetUserId(),
		Message:     log.Message,
		TimeStamp:   timestamp,
		SequenceNum: log.SequenceNum,
		Stream:      log.Stream,
		Retention:   workflow.GetLogRetention(),
	}
	record, err := joblogevents.KafkaRecord(event)
	if err != nil {
		return err
	}
	if r.kfk == nil {
		return status.Error(codes.FailedPrecondition, "kafka client is not configured")
	}
	if err := r.kfk.ProduceSync(ctx, record).FirstErr(); err != nil {
		return status.Errorf(codes.Unavailable, "failed to publish canceled job log: %v", err)
	}

	return nil
}

func (r *Repository) cleanupJobsWithStatus(parentCtx context.Context, workflow *workflowspb.GetWorkflowByIDResponse, userID, status string) error {
	cursor := ""
	var firstErr error
	for {
		ctx, err := r.withAuthorization(parentCtx)
		if err != nil {
			return err
		}

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

		for _, job := range jobs.GetJobs() {
			if err := r.cleanupCanceledJobContainer(parentCtx, workflow, job); err != nil {
				if firstErr == nil {
					firstErr = err
				}
			}
		}

		if jobs.GetCursor() == "" {
			return firstErr
		}

		cursor = jobs.GetCursor()
	}
}

// cancelJobs cancels the jobs of the workflow with the specified status.
func (r *Repository) cancelJobsWithStatus(parentCtx context.Context, workflow *workflowspb.GetWorkflowByIDResponse, userID, status string) error {
	return r.cancelJobsWithStatusExcept(parentCtx, workflow, userID, status)
}

func (r *Repository) cancelJobsWithStatusExcept(parentCtx context.Context, workflow *workflowspb.GetWorkflowByIDResponse, userID, status string, excludedJobIDs ...string) error {
	return r.cancelJobsWithStatusAndTriggerExcept(parentCtx, workflow, userID, status, "", excludedJobIDs...)
}

func (r *Repository) cancelJobsWithStatusAndTriggerExcept(
	parentCtx context.Context,
	workflow *workflowspb.GetWorkflowByIDResponse,
	userID,
	status,
	trigger string,
	excludedJobIDs ...string,
) error {
	cleanupJobs, err := r.markJobsCanceledWithStatusAndTriggerExcept(
		parentCtx,
		workflow,
		userID,
		status,
		trigger,
		excludedJobIDs...,
	)
	if err != nil {
		return err
	}

	return r.cleanupCanceledJobContainers(parentCtx, workflow, cleanupJobs)
}

func (r *Repository) markJobsCanceledWithStatusAndTriggerExcept(
	parentCtx context.Context,
	workflow *workflowspb.GetWorkflowByIDResponse,
	userID,
	status,
	trigger string,
	excludedJobIDs ...string,
) ([]*jobspb.JobsResponse, error) {
	excluded := make(map[string]struct{}, len(excludedJobIDs))
	for _, id := range excludedJobIDs {
		if id != "" {
			excluded[id] = struct{}{}
		}
	}

	var cleanupJobs []*jobspb.JobsResponse

	// Get all the jobs of the workflow which are in the specified status
	cursor := ""
	for {
		ctx, err := r.withAuthorization(parentCtx)
		if err != nil {
			return nil, err
		}

		jobs, err := r.svc.Jobs.ListJobs(ctx, &jobspb.ListJobsRequest{
			WorkflowId: workflow.GetId(),
			UserId:     userID,
			Cursor:     cursor,
			Filters: &jobspb.ListJobsFilters{
				Status:  status,
				Trigger: trigger,
			},
		})
		if err != nil {
			return nil, err
		}

		if len(jobs.GetJobs()) == 0 {
			break
		}

		canceledJobs, err := r.cancelJobs(ctx, workflow, userID, jobs, excluded)
		if err != nil {
			return nil, err
		}
		cleanupJobs = append(cleanupJobs, canceledJobs...)

		if jobs.GetCursor() == "" {
			break
		}

		cursor = jobs.GetCursor()
	}

	return cleanupJobs, nil
}

// markRunningJobsCanceled marks the running jobs of the workflow as canceled.
func (r *Repository) markRunningJobsCanceled(
	parentCtx context.Context,
	workflow *workflowspb.GetWorkflowByIDResponse,
	userID string,
) ([]*jobspb.JobsResponse, error) {
	var cleanupJobs []*jobspb.JobsResponse

	// Get all the jobs of the workflow which are in the RUNNING state
	cursor := ""
	for {
		ctx, err := r.withAuthorization(parentCtx)
		if err != nil {
			return nil, err
		}

		jobs, err := r.svc.Jobs.ListJobs(ctx, &jobspb.ListJobsRequest{
			WorkflowId: workflow.GetId(),
			UserId:     userID,
			Cursor:     cursor,
			Filters: &jobspb.ListJobsFilters{
				Status: jobsmodel.JobStatusRunning.ToString(),
			},
		})
		if err != nil {
			return nil, err
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

		canceledJobs, err := r.cancelJobs(ctx, workflow, userID, &jobspb.ListJobsResponse{
			Jobs:   jobsToCancel,
			Cursor: jobs.GetCursor(),
		}, nil)
		if err != nil {
			return nil, err
		}
		cleanupJobs = append(cleanupJobs, canceledJobs...)

		if jobs.GetCursor() == "" {
			break
		}

		cursor = jobs.GetCursor()
	}

	return cleanupJobs, nil
}

// terminate workflow terminates the workflow.
func (r *Repository) terminateWorkflow(parentCtx context.Context, workflowEvent *workflowsmodel.WorkflowEvent) error {
	workflowID := workflowEvent.ID
	userID := workflowEvent.UserID

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

	if isStaleWorkflowEvent(workflow, workflowEvent) {
		return nil
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

	// Cancel all the hanging jobs which are in the RUNNING state
	// This is to ensure that we don't leave any jobs running after the workflow is terminated
	cleanupJobs, err := r.markRunningJobsCanceled(ctx, workflow, userID)
	if err != nil {
		return err
	}

	// Cancel all the jobs which are in the QUEUED or PENDING state
	pendingCleanupJobs, err := r.markJobsCanceledWithStatusAndTriggerExcept(
		ctx,
		workflow,
		userID,
		jobsmodel.JobStatusPending.ToString(),
		"",
	)
	if err != nil {
		return err
	}
	cleanupJobs = append(cleanupJobs, pendingCleanupJobs...)

	queuedCleanupJobs, err := r.markJobsCanceledWithStatusAndTriggerExcept(
		ctx,
		workflow,
		userID,
		jobsmodel.JobStatusQueued.ToString(),
		"",
	)
	if err != nil {
		return err
	}
	cleanupJobs = append(cleanupJobs, queuedCleanupJobs...)

	if err := r.cleanupCanceledJobContainers(ctx, workflow, cleanupJobs); err != nil {
		return err
	}

	// Retry container cleanup after active jobs have been moved to CANCELED so
	// one stale cleanup failure cannot leave other active jobs runnable.
	if err := r.cleanupJobsWithStatus(ctx, workflow, userID, jobsmodel.JobStatusCanceled.ToString()); err != nil {
		return err
	}

	r.sendWorkflowTerminatedNotification(parentCtx, userID, workflow, workflowEvent)

	return nil
}
