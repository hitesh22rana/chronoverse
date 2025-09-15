package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	authSubject   = "internal/workflow"
	retryBackoff  = time.Second
	lockKeyPrefix = "workflow"
)

// ContainerSvc represents the container service.
type ContainerSvc interface {
	Build(ctx context.Context, imageName string) error
	Terminate(ctx context.Context, containerID string) error
}

// Services represents the services used by the workflow.
type Services struct {
	Workflows     workflowspb.WorkflowsServiceClient
	Jobs          jobspb.JobsServiceClient
	Notifications notificationspb.NotificationsServiceClient
	Csvc          ContainerSvc
}

// Config represents the repository constants configuration.
type Config struct {
	ParallelismLimit int
}

// kafkaJob defines a job for the worker pool.
type kafkaJob struct {
	record *kgo.Record
}

// Repository provides workflow repository.
type Repository struct {
	tp      trace.Tracer
	cfg     *Config
	auth    auth.IAuth
	rdb     *redis.Store
	ch      *clickhouse.Client
	kfk     *kgo.Client
	svc     *Services
	jobChan chan kafkaJob
	wg      sync.WaitGroup
}

// New creates a new workflow repository.
func New(cfg *Config, auth auth.IAuth, rdb *redis.Store, ch *clickhouse.Client, kfk *kgo.Client, svc *Services) *Repository {
	r := &Repository{
		tp:      otel.Tracer(svcpkg.Info().GetName()),
		cfg:     cfg,
		auth:    auth,
		rdb:     rdb,
		ch:      ch,
		kfk:     kfk,
		svc:     svc,
		jobChan: make(chan kafkaJob, cfg.ParallelismLimit),
	}

	return r
}

// StartWorkers starts the worker goroutines.
func (r *Repository) StartWorkers(ctx context.Context) {
	// Start worker goroutines
	for i := range r.cfg.ParallelismLimit {
		r.wg.Go(func() {
			r.worker(ctx, fmt.Sprintf("worker-%d", i))
		})
	}
}

// worker processes Kafka messages from the job channel.
func (r *Repository) worker(ctx context.Context, workerID string) {
	defer r.wg.Done()

	for {
		select {
		case job, ok := <-r.jobChan:
			if !ok {
				// Channel closed, exit worker
				return
			}

			ctxWithTrace, span := r.tp.Start(
				ctx,
				"workflow.worker.processRecord",
				trace.WithAttributes(
					attribute.String("worker_id", workerID),
					attribute.String("topic", job.record.Topic),
					attribute.Int64("offset", job.record.Offset),
					attribute.Int64("partition", int64(job.record.Partition)),
					attribute.String("key", string(job.record.Key)),
					attribute.String("value", string(job.record.Value)),
				),
			)

			logger := loggerpkg.FromContext(ctxWithTrace).With(zap.String("worker_id", workerID))

			var workflowEntry workflowsmodel.WorkflowEvent
			if err := json.Unmarshal(job.record.Value, &workflowEntry); err != nil {
				logger.Error(
					"failed to unmarshal record",
					zap.Any("ctx", ctxWithTrace),
					zap.String("topic", job.record.Topic),
					zap.Int64("offset", job.record.Offset),
					zap.Int32("partition", job.record.Partition),
					zap.String("value", string(job.record.Value)),
					zap.Error(err),
				)
			} else if workflowErr := r.runTargetWorkflow(ctxWithTrace, workflowEntry); workflowErr != nil {
				if status.Code(workflowErr) == codes.Internal || status.Code(workflowErr) == codes.Unavailable {
					logger.Error(
						"internal error while executing workflow",
						zap.Any("ctx", ctxWithTrace),
						zap.String("topic", job.record.Topic),
						zap.Int64("offset", job.record.Offset),
						zap.Int32("partition", job.record.Partition),
						zap.String("value", string(job.record.Value)),
						zap.Error(workflowErr),
					)
				} else {
					logger.Warn(
						"workflow execution failed",
						zap.Any("ctx", ctxWithTrace),
						zap.String("topic", job.record.Topic),
						zap.Int64("offset", job.record.Offset),
						zap.Int32("partition", job.record.Partition),
						zap.String("value", string(job.record.Value)),
						zap.Error(workflowErr),
					)
				}
			}

			// Commit the record even if the workflow workflow fails to avoid reprocessing
			if err := r.kfk.CommitRecords(ctxWithTrace, job.record); err != nil {
				logger.Error(
					"failed to commit record",
					zap.Any("ctx", ctxWithTrace),
					zap.String("topic", job.record.Topic),
					zap.Int64("offset", job.record.Offset),
					zap.Int32("partition", job.record.Partition),
					zap.String("value", string(job.record.Value)),
					zap.Error(err),
				)
			} else {
				logger.Info("record processed and committed successfully",
					zap.Any("ctx", ctxWithTrace),
					zap.String("topic", job.record.Topic),
					zap.Int64("offset", job.record.Offset),
					zap.Int32("partition", job.record.Partition),
					zap.String("value", string(job.record.Value)),
				)
			}
			span.End()

		case <-ctx.Done():
			// Context canceled, exit worker
			return
		}
	}
}

// runTargetWorkflow runs the target workflow based on the action.
func (r *Repository) runTargetWorkflow(ctx context.Context, workflowEntry workflowsmodel.WorkflowEvent) error {
	switch workflowEntry.Action {
	case workflowsmodel.ActionBuild:
		return r.buildWorkflow(ctx, workflowEntry.ID)
	case workflowsmodel.ActionTerminate:
		return r.terminateWorkflow(ctx, workflowEntry.ID, workflowEntry.UserID)
	case workflowsmodel.ActionDelete:
		return r.deleteWorkflow(ctx, workflowEntry.ID, workflowEntry.UserID)
	default:
		return status.Errorf(codes.InvalidArgument, "unknown workflow action: %s", workflowEntry.Action)
	}
}

// Run start the workflow execution.
func (r *Repository) Run(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)

	// Start workers with the context
	r.StartWorkers(ctx)

	// Ensures that the job channel is closed and all workers are done before returning
	defer func() {
		close(r.jobChan)
		r.wg.Wait()
	}()

	for {
		// Check context cancellation before processing
		select {
		case <-ctx.Done():
			logger.Warn("shutting down workflow worker, context canceled", zap.Error(ctx.Err()))
			return ctx.Err()
		default:
			// Continue processing
		}

		fetches := r.kfk.PollFetches(ctx)
		if fetches.IsClientClosed() {
			logger.Warn("kafka client closed, shutting down workflow worker")
			return nil // Return nil as this is an expected shutdown path
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
		}

		for !iter.Done() {
			record := iter.Next()

			select {
			case r.jobChan <- kafkaJob{record: record}:
				// Job dispatched successfully
			case <-ctx.Done(): // Check for cancellation of the main Run context
				logger.Warn("shutting down dispatcher, context canceled", zap.Error(ctx.Err()))
				return ctx.Err() // Exit if main context is canceled
			}
		}
	}
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

// withAuthorization issues the necessary headers and tokens for authorization.
func (r *Repository) withAuthorization(parentCtx context.Context) (context.Context, error) {
	// Attach the audience and role to the context
	ctx := auth.WithAudience(parentCtx, svcpkg.Info().GetName())
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

// withRetry executes the given function and retries once if it fails with an error
// other than codes.FailedPrecondition.
func withRetry(fn func() error) error {
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
