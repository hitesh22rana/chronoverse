package executor

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	delimiter    = '$'
	authSubject  = "internal/executor"
	retryBackoff = time.Second

	// Statuses for the scheduled job.
	statusRunning   = "RUNNING"
	statusCompleted = "COMPLETED"
	statusFailed    = "FAILED"
	statusCanceled  = "CANCELED"

	// Workflow names.
	workflowHeartBeat = "HEARTBEAT"
	workflowContainer = "CONTAINER"

	containerWorkflowExecutionTimeout = 10 * time.Second
)

// Workflow represents a workflow that can be executed.
type Workflow interface {
	Execute(ctx context.Context) error
}

// ContainerSvc represents the container service.
type ContainerSvc interface {
	Execute(ctx context.Context, timeout time.Duration, image string, cmd []string) (<-chan string, <-chan error, error)
}

// HeartBeatSvc represents the heartbeat service.
type HeartBeatSvc interface {
	Execute(ctx context.Context, payload string) error
}

// Services represents the services used by the executor.
type Services struct {
	Jobs jobspb.JobsServiceClient
	Csvc ContainerSvc
	Hsvc HeartBeatSvc
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
					if err := r.runWorkflow(ctxWithTrace, string(record.Value)); err != nil {
						logger.Error(
							"run workflow execution failed",
							zap.Any("ctx", ctxWithTrace),
							zap.Error(err),
						)
					}

					// Commit the record even if the job workflow fails to avoid reprocessing
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
func (r *Repository) runWorkflow(ctx context.Context, recordValue string) error {
	// Extract the data from the record value
	scheduledJobID, jobID, lastScheduledAt, err := extractDataFromRecordValue(recordValue)
	if err != nil {
		return err
	}

	// Issue necessary headers and tokens for authorization
	ctx, err = r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	// Get the job details
	job, err := r.svc.Jobs.GetJobByID(ctx, &jobspb.GetJobByIDRequest{
		Id: jobID,
	})
	if err != nil {
		return err
	}

	// Early return idempotency checks
	// Ensure the job build status is COMPLETED
	if job.GetBuildStatus() != statusCompleted {
		return status.Error(codes.FailedPrecondition, "job build status is not COMPLETED")
	}

	// Ensure the job is not already terminated
	terminatedAt := job.GetTerminatedAt()
	if terminatedAt != "" {
		// If the job is already terminated, do not execute the workflow and update the scheduled job status from QUEUED to CANCELED
		if _, _err := r.svc.Jobs.UpdateScheduledJobStatus(ctx, &jobspb.UpdateScheduledJobStatusRequest{
			Id:     scheduledJobID,
			Status: statusCanceled,
		}); _err != nil {
			return _err
		}

		return status.Error(codes.FailedPrecondition, "job is already terminated")
	}

	// Schedule a new job based on the last scheduled time and interval
	if _, _err := r.svc.Jobs.ScheduleJob(ctx, &jobspb.ScheduleJobRequest{
		JobId:       jobID,
		UserId:      job.GetUserId(),
		ScheduledAt: lastScheduledAt.Add(time.Minute * time.Duration(job.GetInterval())).Format(time.RFC3339Nano),
	}); _err != nil {
		return _err
	}

	// Update the scheduled job status from QUEUED to RUNNING
	if _, _err := r.svc.Jobs.UpdateScheduledJobStatus(ctx, &jobspb.UpdateScheduledJobStatusRequest{
		Id:     scheduledJobID,
		Status: statusRunning,
	}); _err != nil {
		return _err
	}

	var workflowErr error
	switch job.Kind {
	// Execute the HEARTBEAT workflow
	case workflowHeartBeat:
		workflowErr = retryOnce(func() error {
			return r.svc.Hsvc.Execute(ctx, job.GetPayload())
		})
	// Execute the CONTAINER workflow
	case workflowContainer:
		imageName, cmd, err := extractContainerDetails(job.GetPayload())
		if err != nil {
			return err
		}

		workflowErr = retryOnce(func() error {
			_, _, _err := r.svc.Csvc.Execute(
				ctx,
				containerWorkflowExecutionTimeout,
				imageName,
				cmd,
			)
			return _err
		})
	default:
		return status.Error(codes.InvalidArgument, "invalid job kind")
	}

	if workflowErr != nil {
		// Update the scheduled job status from RUNNING to FAILED
		if _, _err := r.svc.Jobs.UpdateScheduledJobStatus(ctx, &jobspb.UpdateScheduledJobStatusRequest{
			Id:     scheduledJobID,
			Status: statusFailed,
		}); _err != nil {
			return _err
		}

		return workflowErr
	}

	// Update the scheduled job status from RUNNING to COMPLETED
	if _, _err := r.svc.Jobs.UpdateScheduledJobStatus(ctx, &jobspb.UpdateScheduledJobStatusRequest{
		Id:     scheduledJobID,
		Status: statusCompleted,
	}); _err != nil {
		return _err
	}

	return nil
}

// extractDataFromRecordValue extracts the data from the record value.
func extractDataFromRecordValue(recordValue string) (scheduledJobID, jobID string, lastScheduledAt time.Time, err error) {
	parts := strings.Split(recordValue, string(delimiter))
	if len(parts) != 3 {
		return "", "", time.Time{}, status.Error(codes.InvalidArgument, "invalid data format")
	}

	lastScheduledAt, err = time.Parse(time.RFC3339Nano, parts[2])
	if err != nil {
		return "", "", time.Time{}, status.Error(codes.InvalidArgument, "invalid scheduled at time")
	}

	return parts[0], parts[1], lastScheduledAt, nil
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

// retryOnce executes the given function and retries once if it fails with an error
// other than codes.FailedPrecondition.
func retryOnce(fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}

	// If the error is FailedPrecondition or InvalidArgument, do not retry
	if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.InvalidArgument {
		return err
	}

	// Wait for the retry backoff duration
	time.Sleep(retryBackoff)

	// Execute the function again
	return fn()
}

// extractContainerDetails extracts the container details from the job payload.
func extractContainerDetails(payload string) (imageName string, cmdStr []string, err error) {
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return "", nil, status.Error(codes.InvalidArgument, "invalid payload format")
	}

	imageName, ok := data["image"].(string)
	if !ok {
		return "", nil, status.Error(codes.InvalidArgument, "image is missing or invalid")
	}

	cmd, ok := data["cmd"].([]any)
	if !ok {
		return "", nil, status.Error(codes.InvalidArgument, "cmd is missing or invalid")
	}

	for _, c := range cmd {
		c, err := c.(string)
		if !err {
			return "", nil, status.Error(codes.InvalidArgument, "cmd is invalid")
		}

		cmdStr = append(cmdStr, c)
	}

	return imageName, cmdStr, nil
}
