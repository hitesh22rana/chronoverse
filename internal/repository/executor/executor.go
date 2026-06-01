package executor

import (
	"context"
	"encoding/json"
	"math"
	"strings"
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
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	authSubject               = "internal/executor"
	retryBackoff              = time.Second
	containerLogReplayTimeout = 2 * time.Minute
)

// ContainerSvc represents the container service.
type ContainerSvc interface {
	Execute(ctx context.Context, timeout time.Duration, image string, cmd, env []string) (string, <-chan *jobsmodel.JobLog, <-chan error, error)
	Logs(ctx context.Context, containerID string) (<-chan *jobsmodel.JobLog, <-chan error, error)
	Inspect(ctx context.Context, containerID string) (*container.State, error)
	Remove(ctx context.Context, containerID string) error
	Terminate(ctx context.Context, containerID string) error
}

// HeartBeatSvc represents the heartbeat service.
type HeartBeatSvc interface {
	Execute(ctx context.Context, timeout time.Duration, endpoint string, expectedStatusCode int, headers map[string][]string) error
}

// Services represents the services used by the executor.
type Services struct {
	Workflows workflowspb.WorkflowsServiceClient
	Jobs      jobspb.JobsServiceClient
	Csvc      ContainerSvc
	Hsvc      HeartBeatSvc
}

// Config represents the execution worker configuration.
type Config struct {
	WorkerID             string
	Concurrency          int
	LeaseDuration        time.Duration
	LeaseRenewInterval   time.Duration
	SystemRetryLimit     int
	SystemRetryBackoff   time.Duration
	RecoveryInterval     time.Duration
	RecoveryBatchSize    int32
	JobLogBatchSize      int
	JobLogBatchInterval  time.Duration
	JobLogPublishTimeout time.Duration
	JobLogPublishRetries int
	JobLogPublishBackoff time.Duration
	JobLogLiveTimeout    time.Duration
	JobLogLiveBufferSize int
}

// Repository provides executor repository.
type Repository struct {
	tp            trace.Tracer
	cfg           Config
	kfk           *kgo.Client
	auth          auth.IAuth
	slots         chan struct{}
	livePublisher *joblogevents.LivePublisher

	execWG sync.WaitGroup

	runner *kafka.PartitionRunner
	svc    *Services
}

// New creates a new executor repository.
func New(
	cfg *Config,
	auth auth.IAuth,
	kfk *kgo.Client,
	rdb *redis.Store,
	lifecycle *kafka.PartitionLifecycle,
	svc *Services,
) *Repository {
	normalizedCfg := normalizeConfig(cfg)
	r := &Repository{
		tp:    otel.Tracer(svcpkg.Info().GetName()),
		cfg:   normalizedCfg,
		auth:  auth,
		kfk:   kfk,
		svc:   svc,
		slots: make(chan struct{}, normalizedCfg.Concurrency),
		livePublisher: joblogevents.NewLivePublisher(rdb, joblogevents.LivePublisherConfig{
			BufferSize:     normalizedCfg.JobLogLiveBufferSize,
			PublishTimeout: normalizedCfg.JobLogLiveTimeout,
		}),
	}
	r.runner = kafka.NewPartitionRunner(kfk, r.processRecord, &kafka.PartitionRunnerConfig{
		Name:         "executor.worker",
		RetryBackoff: retryBackoff,
		Tracer:       r.tp,
	}, lifecycle)

	return r
}

// Run starts the executor.
func (r *Repository) Run(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)
	r.runner.SetLogger(logger)

	liveCtx, cancelLive := context.WithCancel(ctx)
	liveDone := r.runLiveJobLogPublisher(liveCtx, logger)

	recoveryCtx, cancelRecovery := context.WithCancel(ctx)
	defer cancelRecovery()

	recoveryDone := make(chan struct{})
	go func() {
		defer close(recoveryDone)
		r.recoverExpiredLeases(recoveryCtx)
	}()

	err := r.runner.Run(ctx)
	cancelRecovery()
	r.execWG.Wait()
	cancelLive()
	if liveDone != nil {
		<-liveDone
	}
	<-recoveryDone
	return err
}

func (r *Repository) processRecord(ctx context.Context, record *kgo.Record) error {
	ctxWithTrace, span := r.tp.Start(
		ctx,
		"executor.worker.processRecord",
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

	scheduledJob, err := extractFieldFromRecordValue(record.Value)
	if err != nil {
		return err
	}

	if slotErr := r.acquireExecutionSlot(ctxWithTrace); slotErr != nil {
		return slotErr
	}

	authCtx, err := r.withAuthorization(ctxWithTrace)
	if err != nil {
		r.releaseExecutionSlot()
		return err
	}

	claim, err := r.svc.Jobs.ClaimJob(authCtx, &jobspb.ClaimJobRequest{
		Id:                   scheduledJob.jobID,
		WorkflowId:           scheduledJob.workflowID,
		WorkerId:             r.cfg.WorkerID,
		LeaseDurationSeconds: int32(r.cfg.LeaseDuration.Seconds()),
		DispatchAttempt:      scheduledJob.dispatchAttempt,
	})
	if err != nil {
		r.releaseExecutionSlot()
		return err
	}
	if !claim.GetClaimed() {
		r.releaseExecutionSlot()
		logger.Info("job dispatch skipped",
			zap.String("topic", record.Topic),
			zap.Int64("offset", record.Offset),
			zap.Int32("partition", record.Partition),
			zap.String("job_id", scheduledJob.jobID),
			zap.String("workflow_id", scheduledJob.workflowID),
			zap.String("reason", claim.GetReason()),
		)
		return nil
	}

	r.execWG.Go(func() {
		defer r.releaseExecutionSlot()

		execCtx := r.newExecutionContext(ctxWithTrace)
		if runErr := r.runClaimedWorkflow(execCtx, claim, scheduledJob.lastScheduledAt, scheduledJob.workflowGeneration); runErr != nil {
			logger.Warn("claimed job execution finished with error",
				zap.String("job_id", claim.GetId()),
				zap.String("workflow_id", claim.GetWorkflowId()),
				zap.String("lease_token", claim.GetLeaseToken()),
				zap.Error(runErr),
			)
		}
	})

	logger.Info("job claimed and handed off",
		zap.String("topic", record.Topic),
		zap.Int64("offset", record.Offset),
		zap.Int32("partition", record.Partition),
		zap.String("job_id", scheduledJob.jobID),
		zap.String("workflow_id", scheduledJob.workflowID),
	)

	return nil
}

type scheduledJobRecord struct {
	jobID              string
	workflowID         string
	lastScheduledAt    time.Time
	dispatchAttempt    int32
	workflowGeneration int64
}

// runClaimedWorkflow runs a job that has already been durably claimed.
//

func (r *Repository) runClaimedWorkflow(
	parentCtx context.Context,
	claim *jobspb.ClaimJobResponse,
	lastScheduledAt time.Time,
	workflowGeneration int64,
) (err error) {
	ctx, span := r.tp.Start(
		parentCtx,
		"executor.worker.runClaimedWorkflow",
		trace.WithAttributes(
			attribute.String("job_id", claim.GetId()),
			attribute.String("workflow_id", claim.GetWorkflowId()),
			attribute.String("worker_id", r.cfg.WorkerID),
			attribute.Int("attempts", int(claim.GetAttempts())),
		),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	renewDone := make(chan error, 1)
	go r.renewLeaseLoop(ctx, claim.GetId(), claim.GetLeaseToken(), renewDone)
	go func() {
		if renewErr := <-renewDone; renewErr != nil {
			cancel()
		}
	}()

	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return r.releaseClaimForSystemRetry(ctx, claim, err)
	}

	workflow, err := r.svc.Workflows.GetWorkflowByID(authCtx, &workflowspb.GetWorkflowByIDRequest{
		Id: claim.GetWorkflowId(),
	})
	if err != nil {
		return r.releaseClaimForSystemRetry(ctx, claim, err)
	}

	if workflow.GetTerminatedAt() != "" {
		return r.cancelClaimedJob(ctx, claim)
	}
	if workflow.GetBuildStatus() != workflowsmodel.WorkflowBuildStatusCompleted.ToString() {
		return r.releaseClaimForSystemRetry(ctx, claim, status.Error(codes.FailedPrecondition, "workflow build status is not COMPLETED"))
	}

	switch claim.GetTrigger() {
	case jobsmodel.JobTriggerAutomatic.ToString():
		if err = r.scheduleNextAutomaticJob(authCtx, claim, workflow, lastScheduledAt, workflowGeneration); err != nil {
			return r.releaseClaimForSystemRetry(ctx, claim, err)
		}
	case jobsmodel.JobTriggerManual.ToString():
	default:
		return r.failClaimedJob(ctx, claim, status.Errorf(codes.FailedPrecondition, "unknown job trigger: %s", claim.GetTrigger()), "")
	}

	containerID, executeErr := r.executeWorkflow(ctx, claim.GetId(), claim.GetLeaseToken(), claim.GetAttempts(), workflow)
	if executeErr != nil {
		return r.failClaimedJob(ctx, claim, executeErr, containerID)
	}

	return r.completeClaimedJob(ctx, claim, containerID)
}

func (r *Repository) scheduleNextAutomaticJob(
	ctx context.Context,
	claim *jobspb.ClaimJobResponse,
	workflow *workflowspb.GetWorkflowByIDResponse,
	lastScheduledAt time.Time,
	workflowGeneration int64,
) error {
	_, err := r.svc.Jobs.ScheduleJob(ctx, &jobspb.ScheduleJobRequest{
		WorkflowId:         claim.GetWorkflowId(),
		UserId:             workflow.GetUserId(),
		ScheduledAt:        lastScheduledAt.Add(time.Minute * time.Duration(workflow.GetInterval())).Format(time.RFC3339Nano),
		Trigger:            jobsmodel.JobTriggerAutomatic.ToString(),
		WorkflowGeneration: workflowGeneration,
	})
	if status.Code(err) == codes.FailedPrecondition && workflowGeneration > 0 {
		loggerpkg.FromContext(ctx).Info("skipped stale automatic follow-up schedule",
			zap.String("job_id", claim.GetId()),
			zap.String("workflow_id", claim.GetWorkflowId()),
			zap.Int64("workflow_generation", workflowGeneration),
			zap.Error(err),
		)
		return nil
	}

	return err
}

func (r *Repository) acquireExecutionSlot(ctx context.Context) error {
	select {
	case r.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Repository) releaseExecutionSlot() {
	select {
	case <-r.slots:
	default:
	}
}

func (r *Repository) newExecutionContext(parent context.Context) context.Context {
	return context.WithoutCancel(parent)
}

func (r *Repository) renewLeaseLoop(ctx context.Context, jobID, leaseToken string, done chan<- error) {
	ticker := time.NewTicker(r.cfg.LeaseRenewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			authCtx, err := r.withAuthorization(ctx)
			if err != nil {
				done <- err
				return
			}
			if _, err = r.svc.Jobs.RenewJobLease(authCtx, &jobspb.RenewJobLeaseRequest{
				Id:                   jobID,
				LeaseToken:           leaseToken,
				LeaseDurationSeconds: int32(r.cfg.LeaseDuration.Seconds()),
			}); err != nil {
				done <- err
				return
			}
		}
	}
}

func (r *Repository) cancelClaimedJob(ctx context.Context, claim *jobspb.ClaimJobResponse) error {
	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	_, err = r.svc.Jobs.CancelClaimedJob(authCtx, &jobspb.CancelClaimedJobRequest{
		Id:         claim.GetId(),
		LeaseToken: claim.GetLeaseToken(),
	})
	return err
}

func (r *Repository) releaseClaimForSystemRetry(ctx context.Context, claim *jobspb.ClaimJobResponse, cause error) error {
	if int(claim.GetAttempts()) >= r.cfg.SystemRetryLimit {
		return r.failClaimedJob(ctx, claim, cause, "")
	}

	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	nextAttemptAt := time.Now().Add(r.systemRetryBackoff(claim.GetAttempts())).Format(time.RFC3339Nano)
	_, err = r.svc.Jobs.ReleaseJobForRetry(authCtx, &jobspb.ReleaseJobForRetryRequest{
		Id:            claim.GetId(),
		LeaseToken:    claim.GetLeaseToken(),
		NextAttemptAt: nextAttemptAt,
		ErrorCode:     status.Code(cause).String(),
		ErrorMessage:  cause.Error(),
	})
	return err
}

func (r *Repository) failClaimedJob(
	ctx context.Context,
	claim *jobspb.ClaimJobResponse,
	executeErr error,
	containerID string,
) error {
	decision := classifyExecutionFailure(executeErr)
	if decision.Retryable && int(claim.GetAttempts()) < r.cfg.SystemRetryLimit {
		retryCtx := context.WithoutCancel(ctx)
		if cleanupErr := r.cleanupContainer(retryCtx, containerID); cleanupErr != nil {
			loggerpkg.FromContext(ctx).Warn("failed to cleanup container before retry",
				zap.String("job_id", claim.GetId()),
				zap.String("container_id", containerID),
				zap.Error(cleanupErr),
			)
		}
		return r.releaseClaimForSystemRetry(retryCtx, claim, executeErr)
	}

	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	_, err = r.svc.Jobs.FailJob(authCtx, &jobspb.FailJobRequest{
		Id:           claim.GetId(),
		LeaseToken:   claim.GetLeaseToken(),
		FailureKind:  decision.Kind,
		ErrorCode:    status.Code(executeErr).String(),
		ErrorMessage: executeErr.Error(),
	})
	if err != nil {
		return err
	}

	if cleanupErr := r.cleanupContainer(context.WithoutCancel(ctx), containerID); cleanupErr != nil {
		loggerpkg.FromContext(ctx).Warn("failed to cleanup container after job failure",
			zap.String("job_id", claim.GetId()),
			zap.String("container_id", containerID),
			zap.Error(cleanupErr),
		)
	}

	return executeErr
}

func (r *Repository) completeClaimedJob(
	ctx context.Context,
	claim *jobspb.ClaimJobResponse,
	containerID string,
) error {
	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	if _, err = r.svc.Jobs.CompleteJob(authCtx, &jobspb.CompleteJobRequest{
		Id:         claim.GetId(),
		LeaseToken: claim.GetLeaseToken(),
	}); err != nil {
		return err
	}

	if cleanupErr := r.cleanupContainer(context.WithoutCancel(ctx), containerID); cleanupErr != nil {
		loggerpkg.FromContext(ctx).Warn("failed to cleanup container after job completion",
			zap.String("job_id", claim.GetId()),
			zap.String("container_id", containerID),
			zap.Error(cleanupErr),
		)
	}

	return nil
}

func (r *Repository) cleanupContainer(ctx context.Context, containerID string) error {
	if containerID == "" {
		return nil
	}

	return r.svc.Csvc.Remove(ctx, containerID)
}

type executionFailureDecision struct {
	Retryable bool
	Kind      string
}

func classifyExecutionFailure(err error) executionFailureDecision {
	if err == nil {
		return executionFailureDecision{Kind: jobsmodel.FailureKindUser.ToString()}
	}

	switch status.Code(err) { //nolint:exhaustive // Only retryable infrastructure codes need special handling here.
	case codes.Canceled, codes.Internal, codes.Unavailable:
		return executionFailureDecision{Retryable: true, Kind: jobsmodel.FailureKindSystem.ToString()}
	case codes.DeadlineExceeded:
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "context canceled") {
			return executionFailureDecision{Retryable: true, Kind: jobsmodel.FailureKindSystem.ToString()}
		}
		if strings.Contains(message, "container execution timed out") {
			return executionFailureDecision{Kind: jobsmodel.FailureKindUser.ToString()}
		}
		return executionFailureDecision{Retryable: true, Kind: jobsmodel.FailureKindSystem.ToString()}
	default:
		return executionFailureDecision{Kind: jobsmodel.FailureKindUser.ToString()}
	}
}

func (r *Repository) systemRetryBackoff(attempt int32) time.Duration {
	if attempt <= 1 {
		return r.cfg.SystemRetryBackoff
	}

	multiplier := math.Pow(2, float64(attempt-1))
	backoff := time.Duration(float64(r.cfg.SystemRetryBackoff) * multiplier)
	maxBackoff := r.cfg.SystemRetryBackoff * 8
	if backoff > maxBackoff {
		return maxBackoff
	}

	return backoff
}

// extractFieldFromRecordValue extracts the data from the record value.
func extractFieldFromRecordValue(recordValue []byte) (scheduledJobRecord, error) {
	var scheduledJobEntry jobsmodel.ScheduledJobEntry
	if err := json.Unmarshal(recordValue, &scheduledJobEntry); err != nil {
		return scheduledJobRecord{}, status.Error(codes.InvalidArgument, "invalid record value format")
	}

	if scheduledJobEntry.DispatchAttempt <= 0 {
		return scheduledJobRecord{}, status.Error(codes.InvalidArgument, "invalid dispatch attempt")
	}
	lastScheduledAt, err := time.Parse(time.RFC3339Nano, scheduledJobEntry.ScheduledAt)
	if err != nil {
		return scheduledJobRecord{}, status.Error(codes.InvalidArgument, "invalid scheduledAt format")
	}

	return scheduledJobRecord{
		jobID:              scheduledJobEntry.JobID,
		workflowID:         scheduledJobEntry.WorkflowID,
		lastScheduledAt:    lastScheduledAt,
		dispatchAttempt:    scheduledJobEntry.DispatchAttempt,
		workflowGeneration: scheduledJobEntry.WorkflowGeneration,
	}, nil
}

// withAuthorization issues the necessary headers and tokens for authorization.
func (r *Repository) withAuthorization(parentCtx context.Context) (context.Context, error) {
	return auth.WithInternalServiceAuthorization(parentCtx, r.auth, authSubject)
}

// executeWorkflow executes the workflow.
func (r *Repository) executeWorkflow(
	ctx context.Context,
	jobID,
	leaseToken string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
) (string, error) {
	switch workflow.GetKind() {
	// Execute the HEARTBEAT workflow
	case workflowsmodel.KindHeartbeat.ToString():
		return "", r.executeHeartbeatWorkflow(ctx, workflow)
	// Execute the CONTAINER workflow
	case workflowsmodel.KindContainer.ToString():
		return r.executeContainerWorkflow(ctx, jobID, leaseToken, attempts, workflow)
	default:
		return "", status.Error(codes.InvalidArgument, "invalid workflow kind")
	}
}
