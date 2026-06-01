package executor

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func (r *Repository) recoverExpiredLeases(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.RecoveryInterval)
	defer ticker.Stop()

	for {
		r.recoverExpiredLeaseBatch(ctx)

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
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	renewDone := make(chan error, 1)
	go r.renewLeaseLoop(ctx, claim.GetId(), claim.GetLeaseToken(), renewDone)

	renewStopped := make(chan struct{})
	go func() {
		defer close(renewStopped)
		if renewErr := <-renewDone; renewErr != nil {
			loggerpkg.FromContext(parentCtx).Warn("recovered job lease renewal failed",
				zap.String("job_id", claim.GetId()),
				zap.String("workflow_id", claim.GetWorkflowId()),
				zap.String("lease_token", claim.GetLeaseToken()),
				zap.Error(renewErr),
			)
			cancel()
		}
	}()

	err := recoverFn(ctx)
	cancel()
	<-renewStopped
	return err
}

func (r *Repository) recoverExpiredLeaseWithClaim(
	ctx context.Context,
	job *jobspb.ExpiredJobLease,
	claim *jobspb.ClaimJobResponse,
) error {
	workflowForLogs := workflowFromExpiredLease(job)
	if job.GetContainerId() == "" {
		return r.releaseOrFailRecoveredSystem(ctx, claim, status.Error(codes.Unavailable, "job lease expired without a container id"))
	}

	state, err := r.svc.Csvc.Inspect(ctx, job.GetContainerId())
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return r.releaseOrFailRecoveredSystem(ctx, claim, err)
		}
		return err
	}

	if state.Running {
		if terminateErr := r.svc.Csvc.Terminate(ctx, job.GetContainerId()); terminateErr != nil {
			loggerpkg.FromContext(ctx).Warn("failed to terminate expired leased container",
				zap.String("job_id", job.GetId()),
				zap.String("container_id", job.GetContainerId()),
				zap.Error(terminateErr),
			)
			return terminateErr
		}
		if logErr := r.replayContainerLogs(ctx, claim, workflowForLogs, job.GetContainerId()); logErr != nil {
			return logErr
		}
		if removeErr := r.svc.Csvc.Remove(ctx, job.GetContainerId()); removeErr != nil {
			loggerpkg.FromContext(ctx).Warn("failed to remove expired leased container",
				zap.String("job_id", job.GetId()),
				zap.String("container_id", job.GetContainerId()),
				zap.Error(removeErr),
			)
			return removeErr
		}
		return r.releaseOrFailRecoveredSystem(ctx, claim, status.Error(codes.Unavailable, "job lease expired while container was still running"))
	}

	if logErr := r.replayContainerLogs(ctx, claim, workflowForLogs, job.GetContainerId()); logErr != nil {
		return logErr
	}

	if state.ExitCode == 0 {
		return r.completeClaimedJob(ctx, claim, job.GetContainerId())
	}

	execErr := status.Errorf(codes.Aborted, "container exited with non-zero code during lease recovery: %d", state.ExitCode)
	return r.failClaimedJob(ctx, claim, execErr, job.GetContainerId())
}

func (r *Repository) releaseOrFailRecoveredSystem(
	ctx context.Context,
	claim *jobspb.ClaimJobResponse,
	cause error,
) error {
	if int(claim.GetAttempts()) < r.cfg.SystemRetryLimit {
		return r.releaseClaimForSystemRetry(ctx, claim, cause)
	}

	return r.failClaimedJob(ctx, claim, cause, "")
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

	return int32(value) //nolint:gosec // value is bounded above by maxInt32 before conversion.
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
