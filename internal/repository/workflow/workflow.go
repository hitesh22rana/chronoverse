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

// Repository provides workflow repository.
type Repository struct {
	tp   trace.Tracer
	cfg  *Config
	auth auth.IAuth
	rdb  *redis.Store
	ch   *clickhouse.Client
	kfk  *kgo.Client
	svc  *Services
}

// New creates a new workflow repository.
func New(cfg *Config, auth auth.IAuth, rdb *redis.Store, ch *clickhouse.Client, kfk *kgo.Client, svc *Services) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		cfg:  cfg,
		auth: auth,
		rdb:  rdb,
		ch:   ch,
		kfk:  kfk,
		svc:  svc,
	}
}

// Run start the workflow execution.
//
//nolint:gocyclo // Ignore the cyclomatic complexity as it is required for the workflow execution
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

					var workflowEntry workflowsmodel.WorkflowEvent
					if err := json.Unmarshal(record.Value, &workflowEntry); err != nil {
						logger.Error(
							"failed to unmarshal record",
							zap.Any("ctx", ctxWithTrace),
							zap.String("topic", record.Topic),
							zap.Int64("offset", record.Offset),
							zap.Int32("partition", record.Partition),
							zap.String("value", string(record.Value)),
							zap.Error(err),
						)

						// Skip the record and commit it to avoid reprocessing
						if err := r.kfk.CommitRecords(ctxWithTrace, record); err != nil {
							logger.Error(
								"failed to commit record",
								zap.Any("ctx", ctxWithTrace),
								zap.String("topic", record.Topic),
								zap.Int64("offset", record.Offset),
								zap.Int32("partition", record.Partition),
								zap.String("value", string(record.Value)),
								zap.Error(err),
							)
						}

						// Skip processing this record
						return nil
					}

					switch workflowEntry.Action {
					case workflowsmodel.ActionBuild:
						// Execute the build workflow
						if err := r.buildWorkflow(ctxWithTrace, workflowEntry.ID); err != nil {
							// If the build workflow is failed due to internal issues, log the error, else log warning
							if status.Code(err) == codes.Internal || status.Code(err) == codes.Unavailable {
								logger.Error(
									"internal error while executing build workflow",
									zap.Any("ctx", ctxWithTrace),
									zap.String("topic", record.Topic),
									zap.Int64("offset", record.Offset),
									zap.Int32("partition", record.Partition),
									zap.String("value", string(record.Value)),
									zap.Error(err),
								)
							} else {
								logger.Warn(
									"build workflow execution failed",
									zap.Any("ctx", ctxWithTrace),
									zap.String("topic", record.Topic),
									zap.Int64("offset", record.Offset),
									zap.Int32("partition", record.Partition),
									zap.String("value", string(record.Value)),
									zap.Error(err),
								)
							}
						}
					case workflowsmodel.ActionTerminate:
						// Execute the terminate workflow
						if err := r.terminateWorkflow(ctxWithTrace, workflowEntry.ID, workflowEntry.UserID); err != nil {
							// If the terminate workflow is failed due to internal issues, log the error, else log warning
							if status.Code(err) == codes.Internal || status.Code(err) == codes.Unavailable {
								logger.Error(
									"internal error while executing terminate workflow",
									zap.Any("ctx", ctxWithTrace),
									zap.String("topic", record.Topic),
									zap.Int64("offset", record.Offset),
									zap.Int32("partition", record.Partition),
									zap.String("value", string(record.Value)),
									zap.Error(err),
								)
							} else {
								logger.Warn(
									"terminate workflow execution failed",
									zap.Any("ctx", ctxWithTrace),
									zap.String("topic", record.Topic),
									zap.Int64("offset", record.Offset),
									zap.Int32("partition", record.Partition),
									zap.String("value", string(record.Value)),
									zap.Error(err),
								)
							}
						}
					case workflowsmodel.ActionDelete:
						// Execute the delete workflow
						if err := r.deleteWorkflow(ctxWithTrace, workflowEntry.ID, workflowEntry.UserID); err != nil {
							// If the delete workflow is failed due to internal issues, log the error, else log warning
							if status.Code(err) == codes.Internal || status.Code(err) == codes.Unavailable {
								logger.Error(
									"internal error while executing delete workflow",
									zap.Any("ctx", ctxWithTrace),
									zap.String("topic", record.Topic),
									zap.Int64("offset", record.Offset),
									zap.Int32("partition", record.Partition),
									zap.String("value", string(record.Value)),
									zap.Error(err),
								)
							} else {
								logger.Warn(
									"delete workflow execution failed",
									zap.Any("ctx", ctxWithTrace),
									zap.String("topic", record.Topic),
									zap.Int64("offset", record.Offset),
									zap.Int32("partition", record.Partition),
									zap.String("value", string(record.Value)),
									zap.Error(err),
								)
							}
						}
					default:
						logger.Warn("unknown workflow action",
							zap.Any("ctx", ctxWithTrace),
							zap.Any("event", workflowEntry),
						)
					}

					// Commit the record even if the workflow workflow fails to avoid reprocessing
					if err := r.kfk.CommitRecords(ctxWithTrace, record); err != nil {
						logger.Error(
							"failed to commit record",
							zap.Any("ctx", ctxWithTrace),
							zap.String("topic", record.Topic),
							zap.Int64("offset", record.Offset),
							zap.Int32("partition", record.Partition),
							zap.String("value", string(record.Value)),
							zap.Error(err),
						)
					} else {
						logger.Info("record processed and committed successfully",
							zap.Any("ctx", ctxWithTrace),
							zap.String("topic", record.Topic),
							zap.Int64("offset", record.Offset),
							zap.Int32("partition", record.Partition),
							zap.String("value", string(record.Value)),
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
