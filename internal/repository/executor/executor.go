package executor

import (
	"context"
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
	"github.com/hitesh22rana/chronoverse/internal/repository/executor/workflows/heartbeat"
)

const (
	delimiter    = '$'
	authSubject  = "internal/executor"
	retryBackoff = time.Second

	// Statuses for the scheduled job.
	statusRunning   = "RUNNING"
	statusCompleted = "COMPLETED"
	statusFailed    = "FAILED"

	// Workflow names.
	workflowHeartBeat = "HEARTBEAT"
)

// Workflow represents a workflow that can be executed.
type Workflow interface {
	Execute(ctx context.Context) error
}

// Services represents the services used by the executor.
type Services struct {
	Jobs jobspb.JobsServiceClient
}

// Repository provides executor repository.
type Repository struct {
	tp   trace.Tracer
	kfk  *kgo.Client
	auth *auth.Auth
	svc  *Services
}

// New creates a new executor repository.
func New(auth *auth.Auth, svc *Services, kfk *kgo.Client) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
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

		for !iter.Done() {
			record := iter.Next()

			ctxWithTrace, span := r.tp.Start(ctx, "executor.Run")

			// Run the job workflow
			if err := r.runWorkflow(ctxWithTrace, string(record.Value)); err != nil {
				logger.Error(
					"job workflow execution failed",
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
				span.End()
				continue
			}

			logger.Info("record processed",
				zap.Any("ctx", ctxWithTrace),
				zap.String("topic", record.Topic),
				zap.Int64("offset", record.Offset),
				zap.Int32("partition", record.Partition),
				zap.String("message", string(record.Value)),
			)
			span.End()
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

	job, err := r.svc.Jobs.GetJobByID(ctx, &jobspb.GetJobByIDRequest{
		Id: jobID,
	})
	if err != nil {
		return err
	}

	terminatedAt := job.GetTerminatedAt()
	if terminatedAt != "" {
		return status.Error(codes.FailedPrecondition, "job is already terminated")
	}

	// Error group for running multiple goroutines
	eg, groupCtx := errgroup.WithContext(ctx)

	// Schedule a new job based on the last scheduled time and interval
	eg.Go(func() error {
		if _, _err := r.svc.Jobs.ScheduleJob(groupCtx, &jobspb.ScheduleJobRequest{
			JobId:       jobID,
			UserId:      job.GetUserId(),
			ScheduledAt: lastScheduledAt.Add(time.Minute * time.Duration(job.GetInterval())).Format(time.RFC3339Nano),
		}); _err != nil {
			return _err
		}

		return nil
	})

	// Update the scheduled job status from QUEUED to RUNNING
	eg.Go(func() error {
		if _, _err := r.svc.Jobs.UpdateScheduledJobStatus(groupCtx, &jobspb.UpdateScheduledJobStatusRequest{
			Id:     scheduledJobID,
			Status: statusRunning,
		}); _err != nil {
			return _err
		}

		return nil
	})

	// Wait for all the goroutines to finish
	if _err := eg.Wait(); _err != nil {
		return _err
	}

	// Execute the job workflow
	workflow, err := r.getWorkflow(job.GetKind(), job.GetPayload())
	if err != nil {
		return err
	}

	if workflowErr := retryOnce(func() error {
		return workflow.Execute(ctx)
	}); workflowErr != nil {
		// Update the scheduled job status to FAILED
		if _, _err := r.svc.Jobs.UpdateScheduledJobStatus(ctx, &jobspb.UpdateScheduledJobStatusRequest{
			Id:     scheduledJobID,
			Status: statusFailed,
		}); _err != nil {
			return _err
		}

		return err
	}

	// Update the scheduled job status to COMPLETED
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
	ctx = auth.WithRole(ctx, string(auth.RoleAdmin))

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

// getWorkflow returns the workflow based on the job kind.
func (r *Repository) getWorkflow(kind, data string) (Workflow, error) {
	switch kind {
	case workflowHeartBeat:
		return heartbeat.New(data)
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported job kind")
	}
}
