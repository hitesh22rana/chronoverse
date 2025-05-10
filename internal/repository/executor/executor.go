package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	authSubject = "internal/executor"
)

// Workflow represents a workflow that can be executed.
type Workflow interface {
	Execute(ctx context.Context) error
}

// ContainerSvc represents the container service.
type ContainerSvc interface {
	Execute(ctx context.Context, timeout time.Duration, image string, cmd, env []string) (<-chan string, <-chan error, error)
}

// HeartBeatSvc represents the heartbeat service.
type HeartBeatSvc interface {
	Execute(ctx context.Context, payload string) error
}

// Services represents the services used by the executor.
type Services struct {
	Workflows     workflowspb.WorkflowsServiceClient
	Jobs          jobspb.JobsServiceClient
	Notifications notificationspb.NotificationsServiceClient
	Csvc          ContainerSvc
	Hsvc          HeartBeatSvc
}

// Config represents the repository constants configuration.
type Config struct {
	ParallelismLimit int
}

// Repository provides executor repository.
type Repository struct {
	tp   trace.Tracer
	cfg  *Config
	kfk  *kgo.Client
	auth auth.IAuth
	svc  *Services
}

// New creates a new executor repository.
func New(cfg *Config, auth auth.IAuth, svc *Services, kfk *kgo.Client) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		cfg:  cfg,
		auth: auth,
		svc:  svc,
		kfk:  kfk,
	}
}

// Run starts the executor.
func (r *Repository) Run(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)

	for {
		// Check context cancellation before processing
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Continue processing
		}

		fetches := r.kfk.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return status.Error(codes.Canceled, "client closed")
		}

		if fetches.Empty() {
			continue
		}

		iter := fetches.RecordIter()
		for _, fetchErr := range fetches.Errors() {
			logger.Error("error while fetching records",
				zap.String("topic", fetchErr.Topic),
				zap.Int32("partition", fetchErr.Partition),
				zap.Error(fetchErr.Err),
			)
			continue
		}

		// Error group for running multiple goroutines
		eg, groupCtx := errgroup.WithContext(ctx)
		eg.SetLimit(r.cfg.ParallelismLimit)

		for !iter.Done() {
			// Process the record in a separate goroutine
			eg.Go(func(record *kgo.Record) func() error {
				return func() error {
					ctxWithTrace, span := r.tp.Start(groupCtx, "executor.Run")
					defer span.End()

					// Execute the run workflow
					if err := r.runWorkflow(ctxWithTrace, record.Value); err != nil {
						// If the workflow is failed due to internal issues, log the error, else log warning
						if status.Code(err) == codes.Internal || status.Code(err) == codes.Unavailable {
							logger.Error(
								"internal error while executing workflow",
								zap.Any("ctx", ctxWithTrace),
								zap.String("topic", record.Topic),
								zap.Int64("offset", record.Offset),
								zap.Int32("partition", record.Partition),
								zap.String("message", string(record.Value)),
								zap.Error(err),
							)
						} else {
							logger.Warn(
								"error while executing workflow",
								zap.Any("ctx", ctxWithTrace),
								zap.String("topic", record.Topic),
								zap.Int64("offset", record.Offset),
								zap.Int32("partition", record.Partition),
								zap.String("message", string(record.Value)),
								zap.Error(err),
							)
						}
					}

					// Commit the record even if the workflow workflow fails to avoid reprocessing
					if err := r.kfk.CommitRecords(ctxWithTrace, record); err != nil {
						logger.Error(
							"failed to commit record",
							zap.Any("ctx", ctxWithTrace),
							zap.String("topic", record.Topic),
							zap.Int64("offset", record.Offset),
							zap.Int32("partition", record.Partition),
							zap.String("message", string(record.Value)),
							zap.Error(err),
						)
					} else {
						logger.Info("record processed and committed successfully",
							zap.Any("ctx", ctxWithTrace),
							zap.String("topic", record.Topic),
							zap.Int64("offset", record.Offset),
							zap.Int32("partition", record.Partition),
							zap.String("message", string(record.Value)),
						)
					}

					return nil
				}
			}(iter.Next()))
		}

		// Wait for all the goroutines to finish
		if err := eg.Wait(); err != nil {
			logger.Error("error while running goroutines", zap.Error(err))
		}
	}
}

// runWorkflow runs the executor workflow.
//
//nolint:gocyclo // This function is complex and has multiple responsibilities.
func (r *Repository) runWorkflow(ctx context.Context, recordValue []byte) error {
	// Extract the fields from the record value
	jobID, workflowID, lastScheduledAt, err := extractFieldFromRecordValue(recordValue)
	if err != nil {
		return err
	}

	// Issue necessary headers and tokens for authorization
	ctx, err = r.withAuthorization(ctx)
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

	// Early return idempotency checks
	// Ensure the workflow is not already terminated
	if workflow.GetTerminatedAt() != "" {
		// If the workflow is already terminated, do not execute the workflow and update the job status to CANCELED
		if _, err = r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
			Id:     jobID,
			Status: jobsmodel.JobStatusCanceled.ToString(),
		}); err != nil {
			return err
		}

		return status.Error(codes.FailedPrecondition, "workflow is already terminated")
	}

	// Ensure the workflow build status is COMPLETED
	if workflow.GetBuildStatus() != workflowsmodel.WorkflowBuildStatusCompleted.ToString() {
		return status.Error(codes.FailedPrecondition, "workflow build status is not COMPLETED")
	}

	// Ensure the job status is QUEUED, if not return early since the job might be already in progress
	job, err := r.svc.Jobs.GetJobByID(ctx, &jobspb.GetJobByIDRequest{
		Id: jobID,
	})
	if err != nil {
		return err
	}

	// We check if the job status is QUEUED or PENDING
	// If the job status is not QUEUED or PENDING, we don't want to execute the workflow
	// This is to avoid executing the workflow multiple times
	// NOTE: We are checking for the PENDING status because the job status might not be updated yet but have received from kafka via the scheduler
	if job.GetStatus() != jobsmodel.JobStatusQueued.ToString() &&
		job.GetStatus() != jobsmodel.JobStatusPending.ToString() {
		return status.Error(codes.FailedPrecondition, "job is not in QUEUED or PENDING state")
	}

	// Schedule a new job based on the last scheduledAt time and interval accordingly
	if _, err = r.svc.Jobs.ScheduleJob(ctx, &jobspb.ScheduleJobRequest{
		WorkflowId:  workflowID,
		UserId:      workflow.GetUserId(),
		ScheduledAt: lastScheduledAt.Add(time.Minute * time.Duration(workflow.GetInterval())).Format(time.RFC3339Nano),
	}); err != nil {
		return err
	}

	// Update the job status from PENDING/QUEUED to RUNNING
	if _, err = r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
		Id:     jobID,
		Status: jobsmodel.JobStatusRunning.ToString(),
	}); err != nil {
		return err
	}

	executeErr := r.executeWorkflow(ctx, jobID, workflow)

	// Since, the workflow execution can take time to execute and can led to authorization issues
	// So, we need to re-issue the authorization token
	// This context is used for all the gRPC calls
	// This context uses the parent context
	//nolint:errcheck // Ignore the error as we don't want to block the job execution
	ctx, _ = r.withAuthorization(ctx)

	// This context is used for sending notifications, as we don't want to propagate the cancellation
	// This context does not use the parent context
	//nolint:errcheck // Ignore the error as we don't want to block the job execution
	notificationCtx, _ := r.withAuthorization(context.Background())

	//nolint:nestif // This is a nested if statement, but it's necessary to handle the error cases
	if executeErr != nil {
		// Update the job status from RUNNING to FAILED
		if _, err = r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
			Id:     jobID,
			Status: jobsmodel.JobStatusFailed.ToString(),
		}); err != nil {
			return err
		}

		// Increment the workflow failure count and check if the threshold is reached
		res, _err := r.svc.Workflows.IncrementWorkflowConsecutiveJobFailuresCount(ctx, &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
			Id:     workflowID,
			UserId: workflow.GetUserId(),
		})
		if _err != nil {
			return _err
		}

		// Send an error notification for the job execution failure
		// This is a fire-and-forget operation, so we don't need to wait for it to complete
		//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the job execution
		go r.sendNotification(
			notificationCtx,
			workflow.GetUserId(),
			workflowID,
			jobID,
			"Job Execution Failed",
			fmt.Sprintf("Job execution failed for workflow '%s'. Please check the logs for more details.", workflow.GetName()),
			notificationsmodel.KindWebError.ToString(),
			notificationsmodel.EntityJob.ToString(),
		)

		// If the threshold has been reached, terminate the workflow
		if res.GetThresholdReached() {
			if _, err = r.svc.Workflows.TerminateWorkflow(ctx, &workflowspb.TerminateWorkflowRequest{
				Id:     workflowID,
				UserId: workflow.GetUserId(),
			}); err != nil {
				return err
			}

			// The threshold has been reached, send an alert notification for the workflow termination
			// This is a fire-and-forget operation, so we don't need to wait for it to complete
			//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the job execution
			go r.sendNotification(
				notificationCtx,
				workflow.GetUserId(),
				workflowID,
				jobID,
				"Workflow Terminated",
				fmt.Sprintf("Workflow '%s' has been terminated after reaching %d consecutive job failures...", workflow.GetName(), workflow.GetMaxConsecutiveJobFailuresAllowed()),
				notificationsmodel.KindWebAlert.ToString(),
				notificationsmodel.EntityWorkflow.ToString(),
			)
		}

		return err
	}

	// Update the job status from RUNNING to COMPLETED
	if _, err = r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
		Id:     jobID,
		Status: jobsmodel.JobStatusCompleted.ToString(),
	}); err != nil {
		return err
	}

	// Send a success notification for the job execution
	// This is a fire-and-forget operation, so we don't need to wait for it to complete
	//nolint:errcheck,contextcheck // Ignore the error as we don't want to block the job execution
	go r.sendNotification(
		notificationCtx,
		workflow.GetUserId(),
		workflowID,
		jobID,
		"Job Execution Completed",
		fmt.Sprintf("Job execution completed successfully for workflow '%s'.", workflow.GetName()),
		notificationsmodel.KindWebSuccess.ToString(),
		notificationsmodel.EntityJob.ToString(),
	)

	// Reset the workflow consecutive job failures count
	if _, _err := r.svc.Workflows.ResetWorkflowConsecutiveJobFailuresCount(ctx, &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
		Id:     workflowID,
		UserId: workflow.GetUserId(),
	}); _err != nil {
		return _err
	}

	return nil
}

// sendNotification sends a notification for the job execution related events.
func (r *Repository) sendNotification(ctx context.Context, userID, workflowID, jobID, title, message, kind, notificationType string) error {
	switch notificationType {
	case notificationsmodel.EntityJob.ToString():
		payload, err := notificationsmodel.CreateJobsNotificationPayload(title, message, workflowID, jobID)
		if err != nil {
			return err
		}

		// Create a new notification
		if _, err := r.svc.Notifications.CreateNotification(ctx, &notificationspb.CreateNotificationRequest{
			UserId:  userID,
			Kind:    kind,
			Payload: payload,
		}); err != nil {
			return err
		}
	case notificationsmodel.EntityWorkflow.ToString():
		payload, err := notificationsmodel.CreateWorkflowsNotificationPayload(title, message, workflowID)
		if err != nil {
			return err
		}

		// Create a new notification
		if _, err := r.svc.Notifications.CreateNotification(ctx, &notificationspb.CreateNotificationRequest{
			UserId:  userID,
			Kind:    kind,
			Payload: payload,
		}); err != nil {
			return err
		}
	default:
		return status.Error(codes.InvalidArgument, "invalid notification kind")
	}

	return nil
}

// extractFieldFromRecordValue extracts the data from the record value.
func extractFieldFromRecordValue(recordValue []byte) (jobID, workflowID string, lastScheduledAt time.Time, err error) {
	var scheduledJobEntry jobsmodel.ScheduledJobEntry
	if err = json.Unmarshal(recordValue, &scheduledJobEntry); err != nil {
		return "", "", time.Time{}, status.Error(codes.InvalidArgument, "invalid record value format")
	}

	jobID = scheduledJobEntry.JobID
	workflowID = scheduledJobEntry.WorkflowID
	lastScheduledAt, err = time.Parse(time.RFC3339Nano, scheduledJobEntry.ScheduledAt)
	if err != nil {
		return "", "", time.Time{}, status.Error(codes.InvalidArgument, "invalid scheduledAt format")
	}

	return jobID, workflowID, lastScheduledAt, nil
}

// withAuthorization issues the necessary headers and tokens for authorization.
func (r *Repository) withAuthorization(ctx context.Context) (context.Context, error) {
	// Attach the audience and role to the context
	ctx = auth.WithAudience(ctx, svcpkg.Info().GetName())
	ctx = auth.WithRole(ctx, auth.RoleAdmin.String())

	// Issue a new token
	authToken, err := r.auth.IssueToken(ctx, authSubject)
	if err != nil {
		return nil, err
	}

	// Attach all the necessary headers and tokens to the context
	ctx = auth.WithAudienceInMetadata(ctx, svcpkg.Info().GetName())
	ctx = auth.WithRoleInMetadata(ctx, auth.RoleAdmin)
	ctx = auth.WithAuthorizationTokenInMetadata(ctx, authToken)

	return ctx, nil
}

// executeWorkflow executes the workflow.
func (r *Repository) executeWorkflow(ctx context.Context, jobID string, workflow *workflowspb.GetWorkflowByIDResponse) error {
	switch workflow.GetKind() {
	// Execute the HEARTBEAT workflow
	case workflowsmodel.KindHeartbeat.ToString():
		return r.svc.Hsvc.Execute(ctx, workflow.GetPayload())
	// Execute the CONTAINER workflow
	case workflowsmodel.KindContainer.ToString():
		return r.executeContainerWorkflow(ctx, jobID, workflow)
	default:
		return status.Error(codes.InvalidArgument, "invalid workflow kind")
	}
}
