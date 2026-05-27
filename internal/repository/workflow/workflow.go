package workflow

import (
	"context"
	"encoding/json"
	"time"

	"github.com/meilisearch/meilisearch-go"
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

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
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
	Logs(ctx context.Context, containerID string) (<-chan *jobsmodel.JobLog, <-chan error, error)
	Remove(ctx context.Context, containerID string) error
	Terminate(ctx context.Context, containerID string) error
}

// Services represents the services used by the workflow.
type Services struct {
	Workflows     workflowspb.WorkflowsServiceClient
	Jobs          jobspb.JobsServiceClient
	Notifications notificationspb.NotificationsServiceClient
	Csvc          ContainerSvc
}

// Repository provides workflow repository.
type Repository struct {
	tp     trace.Tracer
	auth   auth.IAuth
	rdb    *redis.Store
	ch     *clickhouse.Client
	ms     meilisearch.ServiceManager
	kfk    *kgo.Client
	runner *kafkapkg.PartitionRunner
	svc    *Services
}

// New creates a new workflow repository.
func New(
	auth auth.IAuth,
	rdb *redis.Store,
	ch *clickhouse.Client,
	ms meilisearch.ServiceManager,
	kfk *kgo.Client,
	lifecycle *kafkapkg.PartitionLifecycle,
	svc *Services,
) *Repository {
	r := &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: auth,
		rdb:  rdb,
		ch:   ch,
		ms:   ms,
		kfk:  kfk,
		svc:  svc,
	}
	r.runner = kafkapkg.NewPartitionRunner(kfk, r.processRecord, &kafkapkg.PartitionRunnerConfig{
		Name:         "workflow.worker",
		RetryBackoff: retryBackoff,
		Tracer:       r.tp,
	}, lifecycle)

	return r
}

// runTargetWorkflow runs the target workflow based on the action.
func (r *Repository) runTargetWorkflow(ctx context.Context, workflowEntry *workflowsmodel.WorkflowEvent) error {
	switch workflowEntry.Action {
	case workflowsmodel.ActionBuild:
		return r.buildWorkflow(ctx, workflowEntry)
	case workflowsmodel.ActionReschedule:
		return r.rescheduleWorkflow(ctx, workflowEntry)
	case workflowsmodel.ActionTerminate:
		return r.terminateWorkflow(ctx, workflowEntry)
	case workflowsmodel.ActionDelete:
		return r.deleteWorkflow(ctx, workflowEntry.ID, workflowEntry.UserID)
	case workflowsmodel.ActionJobCompleted:
		return r.handleJobCompleted(ctx, workflowEntry)
	case workflowsmodel.ActionJobFailed:
		return r.handleJobFailed(ctx, workflowEntry)
	default:
		return status.Errorf(codes.InvalidArgument, "unknown workflow action: %s", workflowEntry.Action)
	}
}

func workflowOccurrenceKey(workflowEvent *workflowsmodel.WorkflowEvent) string {
	if workflowEvent.EventKey != "" {
		return workflowEvent.EventKey
	}

	return idempotency.WorkflowEventKey(workflowEvent.ID, workflowEvent.Action.ToString(), workflowEvent.Generation)
}

func isStaleWorkflowEvent(workflow *workflowspb.GetWorkflowByIDResponse, workflowEvent *workflowsmodel.WorkflowEvent) bool {
	return workflowEvent.Generation != 0 && workflow.GetGeneration() != workflowEvent.Generation
}

// Run start the workflow execution.
func (r *Repository) Run(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)
	r.runner.SetLogger(logger)
	return r.runner.Run(ctx)
}

func (r *Repository) processRecord(ctx context.Context, record *kgo.Record) error {
	ctxWithTrace, span := r.tp.Start(
		ctx,
		"workflow.worker.processRecord",
		trace.WithAttributes(
			attribute.String("topic", record.Topic),
			attribute.Int64("offset", record.Offset),
			attribute.Int64("partition", int64(record.Partition)),
			attribute.String("key", string(record.Key)),
			attribute.String("value", string(record.Value)),
		),
	)
	defer span.End()

	logger := loggerpkg.FromContext(ctxWithTrace)

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
		return nil
	}

	workflowErr := r.runTargetWorkflow(ctxWithTrace, &workflowEntry)
	if workflowErr == nil {
		logger.Info("record processed successfully",
			zap.Any("ctx", ctxWithTrace),
			zap.String("topic", record.Topic),
			zap.Int64("offset", record.Offset),
			zap.Int32("partition", record.Partition),
			zap.String("value", string(record.Value)),
		)
		return nil
	}

	fields := []zap.Field{
		zap.Any("ctx", ctxWithTrace),
		zap.String("topic", record.Topic),
		zap.Int64("offset", record.Offset),
		zap.Int32("partition", record.Partition),
		zap.String("value", string(record.Value)),
		zap.Error(workflowErr),
	}
	if status.Code(workflowErr) == codes.Internal || status.Code(workflowErr) == codes.Unavailable {
		logger.Error("internal error while executing workflow", fields...)
	} else {
		logger.Warn("workflow execution failed", fields...)
	}

	return workflowErr
}

// sendNotification sends a notification for the job execution related events.
func (r *Repository) sendNotification(ctx context.Context, userID, workflowID, jobID, title, message, kind, notificationType, occurrenceKey string) error {
	switch notificationType {
	case notificationsmodel.EntityJob.ToString():
		payload, err := notificationsmodel.CreateJobsNotificationPayload(title, message, workflowID, jobID)
		if err != nil {
			return err
		}

		// Create a new notification
		if _, err := r.svc.Notifications.CreateNotification(ctx, &notificationspb.CreateNotificationRequest{
			UserId:         userID,
			Kind:           kind,
			Payload:        payload,
			IdempotencyKey: idempotency.JobNotificationEventKey(jobID, title),
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
			UserId:         userID,
			Kind:           kind,
			Payload:        payload,
			IdempotencyKey: idempotency.WorkflowNotificationEventKey(workflowID, title, occurrenceKey),
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
	return auth.WithInternalServiceAuthorization(parentCtx, r.auth, authSubject)
}
