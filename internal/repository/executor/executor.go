package executor

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
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
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

const (
	authSubject               = "internal/executor"
	retryBackoff              = time.Second
	containerLogReplayTimeout = 2 * time.Minute
)

// ContainerSvc represents the container service.
type ContainerSvc interface {
	Build(ctx context.Context, imageName string) error
	ImageExists(ctx context.Context, imageName string) (bool, error)
	DockerHost() string
	Execute(ctx context.Context, timeout time.Duration, image string, cmd, env []string) (string, <-chan *jobsmodel.JobLog, <-chan error, error)
	Logs(ctx context.Context, containerID string) (<-chan *jobsmodel.JobLog, <-chan error, error)
	Inspect(ctx context.Context, containerID string) (*container.State, error)
	Remove(ctx context.Context, containerID string) error
	Terminate(ctx context.Context, containerID string) error
}

// ContainerSvcFactory creates a container service for a runtime node endpoint.
type ContainerSvcFactory func(runtimeNodeID, endpoint string) (ContainerSvc, error)

// HeartBeatSvc represents the heartbeat service.
type HeartBeatSvc interface {
	Execute(ctx context.Context, timeout time.Duration, endpoint string, expectedStatusCode int, headers map[string][]string) error
}

// Services represents the services used by the executor.
type Services struct {
	Workflows       workflowspb.WorkflowsServiceClient
	Jobs            jobspb.JobsServiceClient
	CsvcForEndpoint ContainerSvcFactory
	Hsvc            HeartBeatSvc
}

// Config represents the execution worker configuration.
type Config struct {
	WorkerID                    string
	Concurrency                 int
	LeaseDuration               time.Duration
	LeaseRenewInterval          time.Duration
	SystemRetryLimit            int
	SystemRetryBackoff          time.Duration
	RecoveryInterval            time.Duration
	RecoveryBatchSize           int32
	JobLogBatchSize             int
	JobLogBatchInterval         time.Duration
	JobLogPublishTimeout        time.Duration
	JobLogPublishRetries        int
	JobLogPublishBackoff        time.Duration
	JobLogLiveTimeout           time.Duration
	JobLogLiveBufferSize        int
	AwaitingReconciliationLimit int
}

// Repository provides executor repository.
type Repository struct {
	tp            trace.Tracer
	cfg           Config
	kfk           *kgo.Client
	auth          auth.IAuth
	slots         chan struct{}
	processID     string
	handoffs      *handoffRegistry
	livePublisher *joblogevents.LivePublisher

	execWG  sync.WaitGroup
	stateMu sync.Mutex

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
) (*Repository, error) {
	normalizedCfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	r := &Repository{
		tp:        otel.Tracer(svcpkg.Info().GetName()),
		cfg:       normalizedCfg,
		auth:      auth,
		kfk:       kfk,
		svc:       svc,
		slots:     make(chan struct{}, normalizedCfg.Concurrency),
		processID: uuid.NewString(),
		handoffs:  newHandoffRegistry(normalizedCfg.AwaitingReconciliationLimit),
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

	return r, nil
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
	r.ensureHandoffState()
	ctxWithTrace, span := r.tp.Start(
		ctx,
		"executor.worker.processRecord",
		trace.WithAttributes(
			attribute.String("topic", record.Topic),
			attribute.Int64("offset", record.Offset),
			attribute.Int64("partition", int64(record.Partition)),
			attribute.String("key", string(record.Key)),
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

	commandID := idempotency.ClaimCommandID(r.processID, scheduledJob.jobID, scheduledJob.dispatchAttempt)
	claimRequest := &jobspb.ClaimJobRequest{
		Id:                   scheduledJob.jobID,
		WorkflowId:           scheduledJob.workflowID,
		WorkerId:             r.cfg.WorkerID,
		LeaseDurationSeconds: int32(r.cfg.LeaseDuration.Seconds()),
		DispatchAttempt:      scheduledJob.dispatchAttempt,
		CommandId:            commandID,
		ProcessInstanceId:    r.processID,
	}
	entry, owner, reserveErr := r.handoffs.getOrReserve(commandID, claimRequest)
	if reserveErr != nil {
		r.releaseExecutionSlot()
		return reserveErr
	}
	if !owner {
		r.releaseExecutionSlot()
		return r.handoffs.wait(ctxWithTrace, entry)
	}

	claim, err := r.svc.Jobs.ClaimJob(authCtx, claimRequest)
	if err != nil {
		r.handoffs.resolveRemoved(entry, err)
		r.releaseExecutionSlot()
		return err
	}
	if !claim.GetClaimed() {
		r.handoffs.resolveRemoved(entry, nil)
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
	if !r.handoffs.activate(entry, claim) {
		r.releaseExecutionSlot()
		return status.Error(codes.Internal, "claim handoff placeholder ownership was lost")
	}

	r.execWG.Go(func() {
		defer r.releaseExecutionSlot()

		execCtx := r.newExecutionContext(ctxWithTrace)
		if runErr := r.runClaimedWorkflow(execCtx, claim, scheduledJob.lastScheduledAt, scheduledJob.workflowGeneration); runErr != nil {
			logger.Warn("claimed job execution finished with error",
				zap.String("job_id", claim.GetId()),
				zap.String("workflow_id", claim.GetWorkflowId()),
				zap.Error(runErr),
			)
		}
		r.handoffs.markAwaiting(commandID)
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

func (r *Repository) ensureHandoffState() {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.processID == "" {
		r.processID = uuid.NewString()
	}
	if r.handoffs == nil {
		limit := r.cfg.AwaitingReconciliationLimit
		if limit <= 0 {
			limit = max(1, r.cfg.Concurrency)
		}
		r.handoffs = newHandoffRegistry(limit)
	}
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
	if renewErr := r.renewLease(ctx, claim.GetId(), claim.GetLeaseToken()); renewErr != nil {
		return renewErr
	}

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
		return r.failClaimedJob(ctx, claim, nil, status.Errorf(codes.FailedPrecondition, "unknown job trigger: %s", claim.GetTrigger()), "")
	}

	var csvc ContainerSvc
	if workflow.GetKind() == workflowsmodel.KindContainer.ToString() {
		csvc, err = r.containerSvcForClaim(claim)
		if err != nil {
			return r.releaseClaimForSystemRetry(ctx, claim, err)
		}
	}

	containerID, executeErr := r.executeWorkflow(ctx, csvc, claim.GetId(), claim.GetLeaseToken(), claim.GetRuntimeNodeId(), claim.GetAttempts(), workflow)
	if executeErr != nil {
		return r.failClaimedJob(ctx, claim, csvc, executeErr, containerID)
	}

	return r.completeClaimedJob(ctx, claim, csvc, containerID)
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
			if err := r.renewLease(ctx, jobID, leaseToken); err != nil {
				done <- err
				return
			}
		}
	}
}

func (r *Repository) renewLease(ctx context.Context, jobID, leaseToken string) error {
	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}
	_, err = r.svc.Jobs.RenewJobLease(authCtx, &jobspb.RenewJobLeaseRequest{
		Id:                   jobID,
		LeaseToken:           leaseToken,
		LeaseDurationSeconds: int32(r.cfg.LeaseDuration.Seconds()),
	})
	return err
}

func (r *Repository) cancelClaimedJob(ctx context.Context, claim *jobspb.ClaimJobResponse) error {
	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	_, err = r.svc.Jobs.CancelClaimedJob(authCtx, &jobspb.CancelClaimedJobRequest{
		Id:                 claim.GetId(),
		LeaseToken:         claim.GetLeaseToken(),
		TerminalReasonCode: terminalreason.WorkflowTerminated.String(),
		CommandId:          uuid.NewString(),
	})
	if err == nil {
		r.handoffs.consume(claim.GetId(), claim.GetLeaseToken())
	}
	return err
}

func (r *Repository) releaseClaimForSystemRetry(ctx context.Context, claim *jobspb.ClaimJobResponse, cause error) error {
	if int(claim.GetAttempts()) >= r.cfg.SystemRetryLimit {
		return r.failClaimedJob(ctx, claim, nil, cause, "")
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
		CommandId:     uuid.NewString(),
	})
	if err == nil {
		r.handoffs.consume(claim.GetId(), claim.GetLeaseToken())
	}
	return err
}

func (r *Repository) failClaimedJob(
	ctx context.Context,
	claim *jobspb.ClaimJobResponse,
	csvc ContainerSvc,
	executeErr error,
	containerID string,
) error {
	decision := classifyExecutionFailure(executeErr)
	if decision.Retryable && int(claim.GetAttempts()) < r.cfg.SystemRetryLimit {
		retryCtx := context.WithoutCancel(ctx)
		if cleanupErr := r.cleanupContainer(retryCtx, csvc, containerID); cleanupErr != nil {
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
		Id:                 claim.GetId(),
		LeaseToken:         claim.GetLeaseToken(),
		FailureKind:        decision.Kind,
		ErrorCode:          status.Code(executeErr).String(),
		ErrorMessage:       executeErr.Error(),
		TerminalReasonCode: decision.ReasonCode,
		CommandId:          uuid.NewString(),
	})
	if err != nil {
		return err
	}
	r.handoffs.consume(claim.GetId(), claim.GetLeaseToken())

	if cleanupErr := r.cleanupContainer(context.WithoutCancel(ctx), csvc, containerID); cleanupErr != nil {
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
	csvc ContainerSvc,
	containerID string,
) error {
	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	if _, err = r.svc.Jobs.CompleteJob(authCtx, &jobspb.CompleteJobRequest{
		Id:         claim.GetId(),
		LeaseToken: claim.GetLeaseToken(),
		CommandId:  uuid.NewString(),
	}); err != nil {
		return err
	}
	r.handoffs.consume(claim.GetId(), claim.GetLeaseToken())

	if cleanupErr := r.cleanupContainer(context.WithoutCancel(ctx), csvc, containerID); cleanupErr != nil {
		loggerpkg.FromContext(ctx).Warn("failed to cleanup container after job completion",
			zap.String("job_id", claim.GetId()),
			zap.String("container_id", containerID),
			zap.Error(cleanupErr),
		)
	}

	return nil
}

func (r *Repository) cleanupContainer(ctx context.Context, csvc ContainerSvc, containerID string) error {
	if containerID == "" {
		return nil
	}
	if csvc == nil {
		return status.Error(codes.FailedPrecondition, "container service is not configured")
	}

	return csvc.Remove(ctx, containerID)
}

type executionFailureDecision struct {
	Retryable  bool
	Kind       string
	ReasonCode string
}

func classifyExecutionFailure(err error) executionFailureDecision {
	if err == nil {
		return executionFailureDecision{Kind: jobsmodel.FailureKindUser.ToString(), ReasonCode: terminalreason.ExecutionFailed.String()}
	}
	if reason, ok := terminalreason.FromError(err); ok {
		return executionFailureDecision{Kind: jobsmodel.FailureKindUser.ToString(), ReasonCode: reason.String()}
	}

	switch status.Code(err) { //nolint:exhaustive // Only retryable infrastructure codes need special handling here.
	case codes.Canceled, codes.Internal, codes.ResourceExhausted, codes.Unavailable:
		return executionFailureDecision{Retryable: true, Kind: jobsmodel.FailureKindSystem.ToString(), ReasonCode: terminalreason.SystemError.String()}
	case codes.DeadlineExceeded:
		return executionFailureDecision{Retryable: true, Kind: jobsmodel.FailureKindSystem.ToString(), ReasonCode: terminalreason.SystemError.String()}
	default:
		return executionFailureDecision{Kind: jobsmodel.FailureKindUser.ToString(), ReasonCode: terminalreason.ExecutionFailed.String()}
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
// The audience set names every service this repository may call.
func (r *Repository) withAuthorization(parentCtx context.Context) (context.Context, error) {
	return auth.WithInternalServiceAuthorization(
		parentCtx,
		r.auth,
		authSubject,
		auth.ServiceNameJobs,
		auth.ServiceNameWorkflows,
	)
}

// executeWorkflow executes the workflow.
func (r *Repository) executeWorkflow(
	ctx context.Context,
	csvc ContainerSvc,
	jobID,
	leaseToken string,
	runtimeNodeID string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
) (string, error) {
	switch workflow.GetKind() {
	// Execute the HEARTBEAT workflow
	case workflowsmodel.KindHeartbeat.ToString():
		return "", r.executeHeartbeatWorkflow(ctx, workflow)
	// Execute the CONTAINER workflow
	case workflowsmodel.KindContainer.ToString():
		return r.executeContainerWorkflow(ctx, csvc, jobID, leaseToken, runtimeNodeID, attempts, workflow)
	default:
		return "", status.Error(codes.InvalidArgument, "invalid workflow kind")
	}
}

func (r *Repository) containerSvcForClaim(claim *jobspb.ClaimJobResponse) (ContainerSvc, error) {
	if claim.GetRuntimeEndpoint() == "" {
		return nil, status.Error(codes.Unavailable, "container job claim has no runtime endpoint")
	}
	if r.svc.CsvcForEndpoint != nil {
		return r.svc.CsvcForEndpoint(claim.GetRuntimeNodeId(), claim.GetRuntimeEndpoint())
	}
	return nil, status.Error(codes.FailedPrecondition, "container service is not configured")
}
