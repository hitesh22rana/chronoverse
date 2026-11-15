package executor

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func (r *Repository) recoverExpiredLeases(ctx context.Context) {
	r.ensureHandoffState()
	ticker := time.NewTicker(r.cfg.RecoveryInterval)
	defer ticker.Stop()

	for {
		r.recoverExpiredLeaseBatch(ctx)
		r.reconcileHandoffs(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Repository) recoverExpiredLeaseBatch(ctx context.Context) {
	logger := loggerpkg.FromContext(ctx)

	remaining := r.recoveryBatchSize()
	claimLimit := r.recoveryClaimLimit()
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return
		}

		waveSize := minInt32(remaining, claimLimit)
		authCtx, err := r.withAuthorization(ctx)
		if err != nil {
			logger.Warn("failed to authorize expired job lease recovery", zap.Error(err))
			return
		}

		res, err := r.svc.Jobs.RecoverExpiredJobLeases(authCtx, &jobspb.RecoverExpiredJobLeasesRequest{
			BatchSize:            waveSize,
			WorkerId:             r.cfg.WorkerID,
			LeaseDurationSeconds: int32(r.cfg.LeaseDuration.Seconds()),
			ProcessInstanceId:    r.processID,
			CommandId:            uuid.NewString(),
		})
		if err != nil {
			logger.Warn("failed to fetch expired job leases", zap.Error(err))
			return
		}

		jobs := res.GetJobs()
		if len(jobs) == 0 {
			return
		}

		r.recoverClaimedExpiredLeases(ctx, jobs)

		claimed := boundedInt32(len(jobs))
		if claimed >= remaining || claimed < waveSize {
			return
		}
		remaining -= claimed
	}
}

func (r *Repository) recoverClaimedExpiredLeases(ctx context.Context, jobs []*jobspb.ExpiredJobLease) {
	var wg sync.WaitGroup
	for _, job := range jobs {
		if job == nil {
			continue
		}

		wg.Go(func() {
			if err := r.recoverExpiredLease(ctx, job); err != nil {
				loggerpkg.FromContext(ctx).Warn("failed to recover expired job lease",
					zap.String("job_id", job.GetId()),
					zap.String("workflow_id", job.GetWorkflowId()),
					zap.String("container_id", job.GetContainerId()),
					zap.Error(err),
				)
			}
		})
	}

	wg.Wait()
}

func (r *Repository) recoverExpiredLease(ctx context.Context, job *jobspb.ExpiredJobLease) error {
	claim := &jobspb.ClaimJobResponse{
		Claimed:     true,
		Id:          job.GetId(),
		WorkflowId:  job.GetWorkflowId(),
		UserId:      job.GetUserId(),
		Trigger:     job.GetTrigger(),
		ScheduledAt: job.GetScheduledAt(),
		Attempts:    job.GetAttempts(),
		LeaseToken:  job.GetLeaseToken(),
	}

	return r.withRecoveredLeaseRenewal(ctx, claim, func(recoveryCtx context.Context) error {
		return r.recoverExpiredLeaseWithClaim(recoveryCtx, job, claim)
	})
}

func (r *Repository) withRecoveredLeaseRenewal(
	parentCtx context.Context,
	claim *jobspb.ClaimJobResponse,
	recoverFn func(context.Context) error,
) error {
	if err := r.renewLease(parentCtx, claim.GetId(), claim.GetLeaseToken()); err != nil {
		return err
	}
	ctx, stopRenewal := r.startLeaseRenewal(parentCtx, claim, "recovery")
	defer stopRenewal()
	// Accepted settlement remains authoritative even if renewal fails during shutdown.
	return recoverFn(ctx)
}

// reconcileHandoffs retains ambiguous executions until PostgreSQL-backed claim
// replay proves that the original lease is inactive or changed. It deliberately
// has no local-time eviction path.
func (r *Repository) reconcileHandoffs(ctx context.Context) {
	for _, entry := range r.handoffs.awaiting() {
		if ctx.Err() != nil {
			return
		}
		authCtx, err := r.withAuthorization(ctx)
		if err != nil {
			return
		}
		claim, err := r.svc.Jobs.ClaimJob(authCtx, entry.request)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				r.handoffs.removeAwaiting(entry)
			}
			continue
		}
		if !claim.GetClaimed() {
			r.handoffs.removeAwaiting(entry)
		}
	}
}

func (r *Repository) recoverExpiredLeaseWithClaim(
	ctx context.Context,
	job *jobspb.ExpiredJobLease,
	claim *jobspb.ClaimJobResponse,
) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	defer func() { err = normalizeExecutionError(err) }()
	workflowForLogs := workflowFromExpiredLease(job)
	if job.GetRuntimeUnavailable() {
		return r.releaseOrFailRecoveredSystem(ctx, claim, status.Error(codes.Unavailable, "runtime node unavailable during lease recovery"))
	}
	if job.GetContainerId() == "" {
		return r.releaseOrFailRecoveredSystem(ctx, claim, status.Error(codes.Unavailable, "job lease expired without a container id"))
	}
	if job.GetRuntimeEndpoint() == "" {
		return r.releaseOrFailRecoveredSystem(ctx, claim, status.Error(codes.Unavailable, "job lease expired without a runtime endpoint"))
	}
	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}
	request := &jobspb.ContainerCleanupReceiptRequest{JobId: claim.GetId(), ContainerId: job.GetContainerId()}
	readCtx, stopRead := context.WithTimeout(authCtx, containerCleanupTimeout)
	receipt, err := r.svc.Jobs.GetContainerCleanupReceipt(readCtx, request)
	stopRead()
	if err != nil {
		return err
	}
	if receipt.GetCompleted() {
		return r.releaseOrFailRecoveredSystem(ctx, claim, status.Error(codes.Unavailable, "container was already cleaned before recovery settlement"))
	}
	csvc, err := r.containerSvcForEndpoint(job.GetRuntimeNodeId(), job.GetRuntimeEndpoint())
	if err != nil {
		return r.releaseOrFailRecoveredSystem(ctx, claim, err)
	}

	state, err := csvc.Inspect(ctx, job.GetContainerId())
	if err != nil {
		if status.Code(err) == codes.NotFound {
			writeCtx, stopWrite := context.WithTimeout(authCtx, containerCleanupTimeout)
			_, receiptErr := r.svc.Jobs.RecordContainerCleanupReceipt(writeCtx, request)
			stopWrite()
			if receiptErr != nil {
				return receiptErr
			}
			return r.releaseOrFailRecoveredSystem(ctx, claim, err)
		}
		return err
	}

	if state.Running {
		return r.recoverRunningContainer(ctx, csvc, job, claim)
	}

	if logErr := r.replayContainerLogs(ctx, csvc, claim, workflowForLogs, job.GetContainerId()); logErr != nil {
		return logErr
	}

	if state.ExitCode == 0 {
		return r.completeClaimedJob(ctx, claim, csvc, job.GetContainerId())
	}

	execErr := terminalreason.Wrap(terminalreason.NonZeroExit, status.Errorf(codes.Aborted, "container exited with non-zero code during lease recovery: %d", state.ExitCode))
	return r.failClaimedJob(ctx, claim, csvc, execErr, job.GetContainerId())
}

func (r *Repository) recoverRunningContainer(ctx context.Context, csvc ContainerSvc, job *jobspb.ExpiredJobLease, claim *jobspb.ClaimJobResponse) error {
	if terminateErr := csvc.Terminate(ctx, job.GetContainerId()); terminateErr != nil {
		loggerpkg.FromContext(ctx).Warn("failed to terminate expired leased container",
			zap.String("job_id", job.GetId()),
			zap.String("container_id", job.GetContainerId()),
			zap.Error(terminateErr),
		)
		return terminateErr
	}
	if logErr := r.replayContainerLogs(ctx, csvc, claim, workflowFromExpiredLease(job), job.GetContainerId()); logErr != nil {
		return logErr
	}
	if removeErr := r.cleanupContainer(ctx, csvc, claim.GetId(), job.GetContainerId()); removeErr != nil {
		loggerpkg.FromContext(ctx).Warn("failed to remove expired leased container",
			zap.String("job_id", job.GetId()),
			zap.String("container_id", job.GetContainerId()),
			zap.Error(removeErr),
		)
		return removeErr
	}
	return r.releaseOrFailRecoveredSystem(ctx, claim, status.Error(codes.Unavailable, "job lease expired while container was still running"))
}

func (r *Repository) containerSvcForEndpoint(runtimeNodeID, endpoint string) (ContainerSvc, error) {
	if endpoint == "" {
		return nil, status.Error(codes.Unavailable, "runtime endpoint is required")
	}
	if r.svc.CsvcForEndpoint != nil {
		return r.svc.CsvcForEndpoint(runtimeNodeID, endpoint)
	}
	return nil, status.Error(codes.FailedPrecondition, "container service is not configured")
}

func (r *Repository) releaseOrFailRecoveredSystem(
	ctx context.Context,
	claim *jobspb.ClaimJobResponse,
	cause error,
) error {
	// Recovery established an infrastructure failure; preserve its original diagnostic code.
	return r.releaseClaimForSystemRetry(ctx, claim, terminalreason.Wrap(terminalreason.SystemError, cause))
}

func (r *Repository) recoveryBatchSize() int32 {
	if r.cfg.RecoveryBatchSize <= 0 {
		return 1
	}

	return r.cfg.RecoveryBatchSize
}

func (r *Repository) recoveryClaimLimit() int32 {
	limit := r.recoveryBatchSize()
	if r.cfg.Concurrency > 0 && int64(r.cfg.Concurrency) < int64(limit) {
		return boundedInt32(r.cfg.Concurrency)
	}

	return limit
}

func boundedInt32(value int) int32 {
	const maxInt32 = int64(1<<31 - 1)

	if value <= 0 {
		return 0
	}
	if int64(value) > maxInt32 {
		return int32(maxInt32)
	}

	return int32(value)
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}

	return b
}

func workflowFromExpiredLease(job *jobspb.ExpiredJobLease) *workflowspb.GetWorkflowByIDResponse {
	if job == nil {
		return nil
	}

	return &workflowspb.GetWorkflowByIDResponse{
		Id:           job.GetWorkflowId(),
		UserId:       job.GetUserId(),
		LogRetention: job.GetLogRetention(),
	}
}
