package workflow

import (
	"context"
	"encoding/json"
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
	authSubject  = "internal/workflow"
	retryBackoff = time.Second

	// Statuses for the job build status.
	statusQueued    = "QUEUED"
	statusStarted   = "STARTED"
	statusCompleted = "COMPLETED"
	statusFailed    = "FAILED"
)

// ContainerSvc represents the container service.
type ContainerSvc interface {
	Build(ctx context.Context, imageName string) error
}

// Services represents the services used by the workflow.
type Services struct {
	Jobs jobspb.JobsServiceClient
	Csvc ContainerSvc
}

// Config represents the repository constants configuration.
type Config struct {
	ParallelismLimit int
}

// Repository provides workflow repository.
type Repository struct {
	tp   trace.Tracer
	cfg  *Config
	auth auth.IAuth
	svc  *Services
	kfk  *kgo.Client
}

// New creates a new workflow repository.
func New(cfg *Config, auth auth.IAuth, svc *Services, kfk *kgo.Client) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		cfg:  cfg,
		auth: auth,
		svc:  svc,
		kfk:  kfk,
	}
}

// Run start the workflow execution.
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
					ctxWithTrace, span := r.tp.Start(groupCtx, "workflow.Run")
					defer span.End()

					// Execute the build workflow
					if err := r.buildWorkflow(ctxWithTrace, string(record.Value)); err != nil {
						logger.Error(
							"build workflow execution failed",
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

// buildWorkflow executes the build workflow.
func (r *Repository) buildWorkflow(ctx context.Context, recordValue string) error {
	jobID, err := extractDataFromRecordValue(recordValue)
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
	// Ensure the build process is not already started
	if job.GetBuildStatus() != statusQueued {
		return nil
	}

	// Ensure the job is not already terminated
	terminatedAt := job.GetTerminatedAt()
	if terminatedAt != "" {
		return status.Error(codes.FailedPrecondition, "job is already terminated")
	}

	// Extract the image from the job
	imageName, err := extractImageName(job.GetPayload())
	if err != nil {
		return err
	}

	// Execute the build process
	if workflowErr := retryOnce(func() error {
		return r.svc.Csvc.Build(ctx, imageName)
	}); workflowErr != nil {
		// Update the job status from QUEUED to FAILED
		if _, _err := r.svc.Jobs.UpdateJobBuildStatus(ctx, &jobspb.UpdateJobBuildStatusRequest{
			Id:          jobID,
			BuildStatus: statusFailed,
		}); _err != nil {
			return _err
		}

		return workflowErr
	}

	// Update the job status from QUEUED to COMPLETED
	if _, _err := r.svc.Jobs.UpdateJobBuildStatus(ctx, &jobspb.UpdateJobBuildStatusRequest{
		Id:          jobID,
		BuildStatus: statusCompleted,
	}); _err != nil {
		return _err
	}

	// Schedule the job for the run
	if _, _err := r.svc.Jobs.ScheduleJob(ctx, &jobspb.ScheduleJobRequest{
		JobId:       jobID,
		UserId:      job.GetUserId(),
		ScheduledAt: time.Now().Add(time.Minute * time.Duration(job.GetInterval())).Format(time.RFC3339Nano),
	}); _err != nil {
		return _err
	}

	return nil
}

// extractDataFromRecordValue extracts the data from the record value.
func extractDataFromRecordValue(recordValue string) (string, error) {
	if recordValue == "" {
		return "", status.Error(codes.InvalidArgument, "record value is empty")
	}

	return recordValue, nil
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

// extractImageName extracts the image name from the job payload.
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
