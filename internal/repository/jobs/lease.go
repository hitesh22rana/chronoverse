package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

const maxJobErrorMessageLength = 4096

type terminalJobSnapshot struct {
	ID          string
	WorkflowID  string
	UserID      string
	StartedAt   sql.NullTime
	CompletedAt time.Time
}

// ClaimJob atomically claims a queued job for execution.
func (r *Repository) ClaimJob(
	ctx context.Context,
	jobID,
	workflowID,
	workerID string,
	leaseDuration time.Duration,
	dispatchAttempt int32,
) (claimed *jobsmodel.ClaimedJob, ok bool, reason string, err error) {
	ctx, span := r.tp.Start(
		ctx,
		"Repository.ClaimJob",
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	leaseToken := fmt.Sprintf("%s:%s", workerID, uuid.NewString())
	query := claimJobQuery()

	claimed = &jobsmodel.ClaimedJob{}
	err = r.pg.QueryRow(
		ctx,
		query,
		jobID,
		workflowID,
		leaseToken,
		workerID,
		leaseSeconds(leaseDuration),
		dispatchAttempt,
		int64(r.cfg.RuntimeHeartbeatTTL.Seconds()),
	).Scan(
		&claimed.ID,
		&claimed.WorkflowID,
		&claimed.UserID,
		&claimed.Trigger,
		&claimed.ScheduledAt,
		&claimed.DispatchAttempts,
		&claimed.Attempts,
		&claimed.LeaseToken,
		&claimed.RuntimeNodeID,
		&claimed.RuntimeEndpoint,
	)
	if err == nil {
		span.SetAttributes(
			attribute.String("job_id", claimed.ID),
			attribute.String("workflow_id", claimed.WorkflowID),
			attribute.Int("attempts", int(claimed.Attempts)),
		)
		return claimed, true, "", nil
	}
	if mappedErr := r.mapJobLeaseReadError(err, "claim job"); mappedErr != nil {
		if status.Code(mappedErr) != grpccodes.NotFound {
			return nil, false, "", mappedErr
		}
	}

	deferred, err := r.deferQueuedJobBlockedFromClaim(ctx, jobID)
	if err != nil {
		return nil, false, "", err
	}
	if deferred {
		return nil, false, "job deferred behind another workflow job", nil
	}

	noRuntime, runtimeErr := r.queuedContainerJobMissingRuntime(ctx, jobID, workflowID, dispatchAttempt)
	if runtimeErr != nil {
		return nil, false, "", runtimeErr
	}
	if noRuntime {
		return nil, false, "", status.Error(grpccodes.Unavailable, "no healthy runtime node is available")
	}

	reason, err = r.jobClaimRejectionReason(ctx, jobID)
	if err != nil {
		return nil, false, "", err
	}

	return nil, false, reason, nil
}

func claimJobQuery() string {
	return fmt.Sprintf(`
        WITH workflow AS (
            SELECT kind
            FROM %s
            WHERE id = $2
        ),
        selected_runtime AS (
            SELECT rn.id, rn.docker_endpoint
            FROM %s AS rn
            WHERE rn.status = 'READY'
                AND rn.last_heartbeat_at > (now() AT TIME ZONE 'utc') - ($7::int * interval '1 second')
                AND rn.running_jobs < rn.max_concurrency
                AND EXISTS (SELECT 1 FROM workflow WHERE kind = 'CONTAINER')
            ORDER BY rn.running_jobs ASC, rn.last_heartbeat_at DESC, rn.id ASC
            FOR UPDATE
            LIMIT 1
        ),
        claimed AS (
        UPDATE %s AS j
        SET status = 'RUNNING',
            attempts = attempts + 1,
            lease_token = $3,
            leased_by = $4,
            lease_expires_at = (now() AT TIME ZONE 'utc') + ($5::int * interval '1 second'),
            last_heartbeat_at = now() AT TIME ZONE 'utc',
            started_at = now() AT TIME ZONE 'utc',
            completed_at = NULL,
			terminal_reason_code = NULL,
            failure_kind = NULL,
            last_error_code = NULL,
            last_error_message = NULL,
            runtime_node_id = CASE
                WHEN (SELECT kind FROM workflow) = 'CONTAINER' THEN (SELECT id FROM selected_runtime)
                ELSE NULL
            END,
            runtime_endpoint = CASE
                WHEN (SELECT kind FROM workflow) = 'CONTAINER' THEN (SELECT docker_endpoint FROM selected_runtime)
                ELSE NULL
            END
        WHERE j.id = $1
            AND j.workflow_id = $2
            AND j.status = 'QUEUED'
            AND j.dispatch_attempts = $6
            AND EXISTS (SELECT 1 FROM workflow)
            AND (
                (SELECT kind FROM workflow) <> 'CONTAINER'
                OR EXISTS (SELECT 1 FROM selected_runtime)
            )
            AND NOT EXISTS (
                SELECT 1
                FROM %s AS active
                WHERE active.workflow_id = j.workflow_id
                    AND active.id <> j.id
                    AND active.status = 'RUNNING'
            )
            AND NOT EXISTS (
                SELECT 1
                FROM %s AS blocker
                WHERE blocker.workflow_id = j.workflow_id
                    AND blocker.id <> j.id
                    AND blocker.status IN ('PENDING', 'QUEUED', 'RUNNING')
                    AND (
                        blocker.scheduled_at < j.scheduled_at
                        OR (blocker.scheduled_at = j.scheduled_at AND blocker.created_at < j.created_at)
                        OR (blocker.scheduled_at = j.scheduled_at AND blocker.created_at = j.created_at AND blocker.id < j.id)
                    )
            )
        RETURNING id, workflow_id, user_id, trigger, scheduled_at, dispatch_attempts, attempts, lease_token, runtime_node_id, runtime_endpoint
        ),
        increment_runtime AS (
            UPDATE %s AS rn
            SET running_jobs = running_jobs + 1,
                updated_at = now() AT TIME ZONE 'utc'
            WHERE rn.id = (SELECT runtime_node_id FROM claimed)
            RETURNING rn.id
        )
        SELECT id, workflow_id, user_id, trigger, scheduled_at, dispatch_attempts, attempts, lease_token, runtime_node_id, runtime_endpoint
        FROM claimed;
    `, postgres.TableWorkflows, postgres.TableRuntimeNodes, postgres.TableJobs, postgres.TableJobs, postgres.TableJobs, postgres.TableRuntimeNodes)
}

// GetReadyRuntimeNode returns a fresh READY runtime node for Docker data plane work.
func (r *Repository) GetReadyRuntimeNode(ctx context.Context) (node *jobsmodel.RuntimeNode, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetReadyRuntimeNode")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := getReadyRuntimeNodeQuery()

	node = &jobsmodel.RuntimeNode{}
	err = r.pg.QueryRow(ctx, query, int64(r.cfg.RuntimeHeartbeatTTL.Seconds())).Scan(
		&node.RuntimeNodeID,
		&node.RuntimeEndpoint,
	)
	if err == nil {
		span.SetAttributes(
			attribute.String("runtime_node_id", node.RuntimeNodeID),
			attribute.String("runtime_endpoint", node.RuntimeEndpoint),
		)
		return node, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(grpccodes.Unavailable, "no healthy runtime node is available")
	}

	return nil, r.mapJobLeaseReadError(err, "get ready runtime node")
}

func getReadyRuntimeNodeQuery() string {
	return fmt.Sprintf(`
		SELECT id, docker_endpoint
		FROM %s
		WHERE status = 'READY'
			AND last_heartbeat_at > (now() AT TIME ZONE 'utc') - ($1::int * interval '1 second')
		ORDER BY running_jobs ASC, last_heartbeat_at DESC, id ASC
		LIMIT 1;
	`, postgres.TableRuntimeNodes)
}

// RenewJobLease renews a running job lease.
func (r *Repository) RenewJobLease(ctx context.Context, jobID, leaseToken string, leaseDuration time.Duration) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.RenewJobLease")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
        UPDATE %s
        SET lease_expires_at = (now() AT TIME ZONE 'utc') + ($3::int * interval '1 second'),
            last_heartbeat_at = now() AT TIME ZONE 'utc'
        WHERE id = $1 AND lease_token = $2 AND status = 'RUNNING';
    `, postgres.TableJobs)

	return r.execLeaseUpdate(ctx, query, "renew job lease", jobID, leaseToken, leaseSeconds(leaseDuration))
}

// AttachJobContainer attaches a Docker container ID to a running claimed job.
func (r *Repository) AttachJobContainer(ctx context.Context, jobID, leaseToken, containerID, runtimeNodeID string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.AttachJobContainer")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
        UPDATE %s
        SET container_id = $3
        WHERE id = $1
            AND lease_token = $2
            AND status = 'RUNNING'
            AND COALESCE(runtime_node_id, '') = $4;
    `, postgres.TableJobs)

	return r.execLeaseUpdate(ctx, query, "attach job container", jobID, leaseToken, containerID, runtimeNodeID)
}

// CompleteJob completes a running claimed job.
func (r *Repository) CompleteJob(ctx context.Context, jobID, leaseToken string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.CompleteJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return r.mapJobLeaseWriteError(err, "start complete job transaction")
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) && err == nil {
			err = r.mapJobLeaseWriteError(rollbackErr, "rollback complete job transaction")
		}
	}()

	query := fmt.Sprintf(`
        UPDATE %s
        SET status = 'COMPLETED',
            completed_at = now() AT TIME ZONE 'utc',
            lease_token = NULL,
            leased_by = NULL,
            lease_expires_at = NULL,
            last_heartbeat_at = NULL
        WHERE id = $1 AND lease_token = $2 AND status = 'RUNNING'
        RETURNING id, workflow_id, user_id, started_at, completed_at;
    `, postgres.TableJobs)

	snapshot, err := r.scanTerminalJobSnapshot(ctx, tx, query, "complete job", jobID, leaseToken)
	if err != nil {
		return err
	}

	if err := r.insertTerminalJobOutboxEvents(ctx, tx, snapshot, workflowsmodel.ActionJobCompleted, "", "", ""); err != nil {
		return err
	}
	if decrementErr := r.decrementRuntimeSlotForJob(ctx, tx, jobID); decrementErr != nil {
		return decrementErr
	}

	if err := tx.Commit(ctx); err != nil {
		return r.mapJobLeaseWriteError(err, "commit complete job transaction")
	}

	return nil
}

// FailJob marks a running claimed job as failed.
func (r *Repository) FailJob(ctx context.Context, jobID, leaseToken, failureKind, errorCode, errorMessage, terminalReasonCode string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.FailJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return r.mapJobLeaseWriteError(err, "start fail job transaction")
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) && err == nil {
			err = r.mapJobLeaseWriteError(rollbackErr, "rollback fail job transaction")
		}
	}()

	query := fmt.Sprintf(`
        UPDATE %s
        SET status = 'FAILED',
            completed_at = now() AT TIME ZONE 'utc',
            lease_token = NULL,
            leased_by = NULL,
            lease_expires_at = NULL,
            last_heartbeat_at = NULL,
            failure_kind = $3,
            last_error_code = $4,
            last_error_message = $5,
			terminal_reason_code = $6
        WHERE id = $1 AND lease_token = $2 AND status = 'RUNNING'
        RETURNING id, workflow_id, user_id, started_at, completed_at;
    `, postgres.TableJobs)

	truncatedMessage := truncateJobError(errorMessage)
	snapshot, err := r.scanTerminalJobSnapshot(ctx, tx, query, "fail job", jobID, leaseToken, failureKind, errorCode, truncatedMessage, terminalReasonCode)
	if err != nil {
		return err
	}

	if err := r.insertTerminalJobOutboxEvents(ctx, tx, snapshot, workflowsmodel.ActionJobFailed, failureKind, errorCode, truncatedMessage); err != nil {
		return err
	}
	if decrementErr := r.decrementRuntimeSlotForJob(ctx, tx, jobID); decrementErr != nil {
		return decrementErr
	}

	if err := tx.Commit(ctx); err != nil {
		return r.mapJobLeaseWriteError(err, "commit fail job transaction")
	}

	return nil
}

// CancelClaimedJob marks a running claimed job as canceled.
func (r *Repository) CancelClaimedJob(ctx context.Context, jobID, leaseToken, terminalReasonCode string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.CancelClaimedJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return r.mapJobLeaseWriteError(err, "start cancel claimed job transaction")
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) && err == nil {
			err = r.mapJobLeaseWriteError(rollbackErr, "rollback cancel claimed job transaction")
		}
	}()

	query := fmt.Sprintf(`
        UPDATE %s
        SET status = 'CANCELED',
            completed_at = now() AT TIME ZONE 'utc',
            lease_token = NULL,
            leased_by = NULL,
            lease_expires_at = NULL,
            last_heartbeat_at = NULL,
			terminal_reason_code = $3
        WHERE id = $1 AND lease_token = $2 AND status = 'RUNNING';
    `, postgres.TableJobs)
	ct, err := tx.Exec(ctx, query, jobID, leaseToken, terminalReasonCode)
	if err != nil {
		return r.mapJobLeaseWriteError(err, "cancel claimed job")
	}
	if ct.RowsAffected() == 0 {
		return status.Errorf(grpccodes.FailedPrecondition, "%s: job lease not held", "cancel claimed job")
	}
	if err := r.decrementRuntimeSlotForJob(ctx, tx, jobID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return r.mapJobLeaseWriteError(err, "commit cancel claimed job transaction")
	}
	return nil
}

// ReleaseJobForRetry releases a running claimed job back to pending for a later retry.
func (r *Repository) ReleaseJobForRetry(ctx context.Context, jobID, leaseToken, nextAttemptAt, errorCode, errorMessage string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ReleaseJobForRetry")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	nextAttemptAtTime, err := parseTime(nextAttemptAt)
	if err != nil {
		return status.Errorf(grpccodes.InvalidArgument, "invalid next_attempt_at time format: %v", err)
	}

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return r.mapJobLeaseWriteError(err, "start release job for retry transaction")
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) && err == nil {
			err = r.mapJobLeaseWriteError(rollbackErr, "rollback release job for retry transaction")
		}
	}()

	var releasedCount int
	var decrementedCount int
	err = tx.QueryRow(
		ctx,
		releaseJobForRetryQuery(),
		jobID,
		leaseToken,
		nextAttemptAtTime,
		jobsmodel.FailureKindSystem.ToString(),
		errorCode,
		truncateJobError(errorMessage),
	).Scan(&releasedCount, &decrementedCount)
	if err != nil {
		return r.mapJobLeaseWriteError(err, "release job for retry")
	}
	if releasedCount == 0 {
		return status.Errorf(grpccodes.FailedPrecondition, "%s: job lease not held", "release job for retry")
	}
	if err := tx.Commit(ctx); err != nil {
		return r.mapJobLeaseWriteError(err, "commit release job for retry transaction")
	}
	return nil
}

// RecoverExpiredJobLeases atomically claims running jobs with expired leases for recovery.
func (r *Repository) RecoverExpiredJobLeases(
	ctx context.Context,
	batchSize int32,
	workerID string,
	leaseDuration time.Duration,
) (jobs []*jobsmodel.ExpiredJobLease, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.RecoverExpiredJobLeases")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	if batchSize <= 0 {
		batchSize = 100
	}
	if workerID == "" {
		workerID = "execution-worker-recovery"
	}

	leaseToken := fmt.Sprintf("%s:%s", workerID, uuid.NewString())
	query := recoverExpiredJobLeasesQuery()

	rows, err := r.pg.Query(ctx, query, batchSize, leaseToken, workerID, leaseSeconds(leaseDuration), int64(r.cfg.RuntimeHeartbeatTTL.Seconds()), int64(r.cfg.RuntimeLostAfter.Seconds()))
	if err != nil {
		if mappedErr := r.mapJobLeaseReadError(err, "recover expired job leases"); mappedErr != nil {
			return nil, mappedErr
		}
	}

	jobs, err = pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[jobsmodel.ExpiredJobLease])
	if err != nil {
		return nil, status.Errorf(grpccodes.Internal, "failed to collect expired job leases: %v", err)
	}

	return jobs, nil
}

func releaseJobForRetryQuery() string {
	return fmt.Sprintf(`
        WITH target AS (
            SELECT id, runtime_node_id
            FROM %s
            WHERE id = $1 AND lease_token = $2 AND status = 'RUNNING'
            FOR UPDATE
        ),
        released AS (
            UPDATE %s AS j
            SET status = 'PENDING',
                queued_at = NULL,
                container_id = NULL,
                runtime_node_id = NULL,
                runtime_endpoint = NULL,
                started_at = NULL,
                completed_at = NULL,
                lease_token = NULL,
                leased_by = NULL,
                lease_expires_at = NULL,
                last_heartbeat_at = NULL,
				terminal_reason_code = NULL,
                next_attempt_at = $3,
                failure_kind = $4,
                last_error_code = $5,
                last_error_message = $6
            FROM target
            WHERE j.id = target.id
            RETURNING target.runtime_node_id AS previous_runtime_node_id
        ),
        decrement_runtime AS (
            UPDATE %s AS rn
            SET running_jobs = GREATEST(0, running_jobs - 1),
                updated_at = now() AT TIME ZONE 'utc'
            FROM released
            WHERE released.previous_runtime_node_id IS NOT NULL
                AND rn.id = released.previous_runtime_node_id
            RETURNING rn.id
        )
        SELECT
            (SELECT COUNT(*) FROM released),
            (SELECT COUNT(*) FROM decrement_runtime);
    `, postgres.TableJobs, postgres.TableJobs, postgres.TableRuntimeNodes)
}

func recoverExpiredJobLeasesQuery() string {
	return fmt.Sprintf(`
        WITH expired AS (
            SELECT j.id
            FROM %s AS j
            LEFT JOIN %s AS rn ON rn.id = j.runtime_node_id
            WHERE j.status = 'RUNNING'
                AND j.lease_token IS NOT NULL
                AND j.lease_expires_at IS NOT NULL
                AND j.lease_expires_at < (now() AT TIME ZONE 'utc')
                AND (
                    j.runtime_node_id IS NULL
                    OR rn.id IS NULL
                    OR rn.status IN ('UNHEALTHY', 'DRAINING')
                    OR (
                        rn.status = 'READY'
                        AND rn.last_heartbeat_at > (now() AT TIME ZONE 'utc') - ($5::int * interval '1 second')
                    )
                    OR rn.last_heartbeat_at <= (now() AT TIME ZONE 'utc') - ($6::int * interval '1 second')
                )
            ORDER BY j.lease_expires_at ASC, j.id ASC
            FOR UPDATE OF j SKIP LOCKED
            LIMIT $1
        )
        UPDATE %s AS j
        SET lease_token = $2,
            leased_by = $3,
            lease_expires_at = (now() AT TIME ZONE 'utc') + ($4::int * interval '1 second'),
            last_heartbeat_at = now() AT TIME ZONE 'utc'
        FROM expired
        JOIN %s AS w ON w.id = (SELECT workflow_id FROM %s WHERE id = expired.id)
        LEFT JOIN %s AS rn ON rn.id = (SELECT runtime_node_id FROM %s WHERE id = expired.id)
        WHERE j.id = expired.id
        RETURNING j.id,
            j.workflow_id,
            j.user_id,
            j.container_id,
            j.lease_token,
            j.leased_by,
            j.trigger,
            j.scheduled_at,
            j.attempts,
            w.log_retention,
            j.runtime_node_id,
            COALESCE(NULLIF(j.runtime_endpoint, ''), rn.docker_endpoint) AS runtime_endpoint,
            -- DRAINING stops new claims but may still allow owner-aware Docker cleanup.
            -- Only missing, unhealthy, or lost runtimes are treated as unavailable.
            (
                j.runtime_node_id IS NOT NULL
                AND (
                    rn.id IS NULL
                    OR rn.status = 'UNHEALTHY'
                    OR rn.last_heartbeat_at <= (now() AT TIME ZONE 'utc') - ($6::int * interval '1 second')
                )
            ) AS runtime_unavailable;
    `, postgres.TableJobs, postgres.TableRuntimeNodes, postgres.TableJobs, postgres.TableWorkflows, postgres.TableJobs, postgres.TableRuntimeNodes, postgres.TableJobs)
}

func (r *Repository) queuedContainerJobMissingRuntime(ctx context.Context, jobID, workflowID string, dispatchAttempt int32) (bool, error) {
	query := queuedContainerJobMissingRuntimeQuery()
	var missing bool
	if err := r.pg.QueryRow(ctx, query, jobID, workflowID, dispatchAttempt, int64(r.cfg.RuntimeHeartbeatTTL.Seconds())).Scan(&missing); err != nil {
		return false, r.mapJobLeaseReadError(err, "check runtime availability")
	}
	return missing, nil
}

func queuedContainerJobMissingRuntimeQuery() string {
	return fmt.Sprintf(`
        SELECT EXISTS (
            SELECT 1
            FROM %s AS j
            JOIN %s AS w ON w.id = j.workflow_id
            WHERE j.id = $1
                AND j.workflow_id = $2
                AND j.status = 'QUEUED'
                AND j.dispatch_attempts = $3
                AND w.kind = 'CONTAINER'
                AND NOT EXISTS (
                    SELECT 1
                    FROM %s AS rn
                    WHERE rn.status = 'READY'
                        AND rn.last_heartbeat_at > (now() AT TIME ZONE 'utc') - ($4::int * interval '1 second')
                        AND rn.running_jobs < rn.max_concurrency
                )
        );
    `, postgres.TableJobs, postgres.TableWorkflows, postgres.TableRuntimeNodes)
}

func (r *Repository) decrementRuntimeSlotForJob(ctx context.Context, tx pgx.Tx, jobID string) error {
	query := fmt.Sprintf(`
        UPDATE %s AS rn
        SET running_jobs = GREATEST(0, running_jobs - 1),
            updated_at = now() AT TIME ZONE 'utc'
        FROM %s AS j
        WHERE j.id = $1
            AND j.runtime_node_id IS NOT NULL
            AND rn.id = j.runtime_node_id;
    `, postgres.TableRuntimeNodes, postgres.TableJobs)
	if _, err := tx.Exec(ctx, query, jobID); err != nil {
		return r.mapJobLeaseWriteError(err, "decrement runtime slot")
	}
	return nil
}

func (r *Repository) deferQueuedJobBlockedFromClaim(ctx context.Context, jobID string) (bool, error) {
	query := fmt.Sprintf(`
        UPDATE %s AS j
        SET status = 'PENDING',
            queued_at = NULL
        WHERE j.id = $1
            AND j.status = 'QUEUED'
            AND EXISTS (
                SELECT 1
                FROM %s AS blocker
                WHERE blocker.workflow_id = j.workflow_id
                    AND blocker.id <> j.id
                    AND (
                        blocker.status = 'RUNNING'
                        OR (
                            blocker.status IN ('PENDING', 'QUEUED', 'RUNNING')
                            AND (
                                blocker.scheduled_at < j.scheduled_at
                                OR (blocker.scheduled_at = j.scheduled_at AND blocker.created_at < j.created_at)
                                OR (blocker.scheduled_at = j.scheduled_at AND blocker.created_at = j.created_at AND blocker.id < j.id)
                            )
                        )
                    )
            );
    `, postgres.TableJobs, postgres.TableJobs)

	ct, err := r.pg.Exec(ctx, query, jobID)
	if err != nil {
		return false, r.mapJobLeaseWriteError(err, "defer blocked queued job")
	}

	return ct.RowsAffected() > 0, nil
}

func (r *Repository) scanTerminalJobSnapshot(
	ctx context.Context,
	tx pgx.Tx,
	query, operation string,
	args ...any,
) (*terminalJobSnapshot, error) {
	snapshot := &terminalJobSnapshot{}
	err := tx.QueryRow(ctx, query, args...).Scan(
		&snapshot.ID,
		&snapshot.WorkflowID,
		&snapshot.UserID,
		&snapshot.StartedAt,
		&snapshot.CompletedAt,
	)
	if err == nil {
		return snapshot, nil
	}
	if r.pg.IsNoRows(err) {
		return nil, status.Errorf(grpccodes.FailedPrecondition, "%s: job lease not held", operation)
	}

	return nil, r.mapJobLeaseWriteError(err, operation)
}

func (r *Repository) insertTerminalJobOutboxEvents(
	ctx context.Context,
	tx pgx.Tx,
	job *terminalJobSnapshot,
	action workflowsmodel.Action,
	failureKind,
	errorCode,
	errorMessage string,
) error {
	duration := job.executionDurationSeconds()
	data, err := json.Marshal(&analyticsmodel.EventTypeJobsData{
		JobExecutionDuration: duration,
	})
	if err != nil {
		return status.Errorf(grpccodes.Internal, "failed to marshal job analytics event data: %v", err)
	}

	analyticsEvent := &analyticsmodel.AnalyticEvent{
		EventKey:   idempotency.JobCompletedAnalyticsEventKey(job.ID),
		UserID:     job.UserID,
		WorkflowID: job.WorkflowID,
		EventType:  analyticsmodel.EventTypeJobs,
		Data:       data,
	}
	if err := outbox.InsertTx(ctx, tx, &outbox.Event{
		Topic:    kafka.TopicAnalytics,
		KafkaKey: job.WorkflowID,
		EventKey: analyticsEvent.EventKey,
		Payload:  analyticsEvent,
	}); err != nil {
		return err
	}

	event := &workflowsmodel.WorkflowEvent{
		EventKey:     idempotency.JobWorkflowEventKey(job.ID, action.ToString()),
		ID:           job.WorkflowID,
		UserID:       job.UserID,
		Action:       action,
		JobID:        job.ID,
		FailureKind:  failureKind,
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
	}
	return outbox.InsertTx(ctx, tx, &outbox.Event{
		Topic:    kafka.TopicWorkflows,
		KafkaKey: job.WorkflowID,
		EventKey: event.EventKey,
		Payload:  event,
	})
}

func (j *terminalJobSnapshot) executionDurationSeconds() uint64 {
	if j == nil || !j.StartedAt.Valid || j.CompletedAt.Before(j.StartedAt.Time) {
		return 0
	}

	return uint64(j.CompletedAt.Sub(j.StartedAt.Time).Seconds())
}

func (r *Repository) execLeaseUpdate(ctx context.Context, query, operation string, args ...any) error {
	ct, err := r.pg.Exec(ctx, query, args...)
	if err != nil {
		return r.mapJobLeaseWriteError(err, operation)
	}
	if ct.RowsAffected() == 0 {
		return status.Errorf(grpccodes.FailedPrecondition, "%s: job lease not held", operation)
	}

	return nil
}

func (r *Repository) jobClaimRejectionReason(ctx context.Context, jobID string) (string, error) {
	query := fmt.Sprintf(`
        SELECT status, dispatch_attempts
        FROM %s
        WHERE id = $1
        LIMIT 1;
    `, postgres.TableJobs)

	var jobStatus string
	var dispatchAttempts int32
	if err := r.pg.QueryRow(ctx, query, jobID).Scan(&jobStatus, &dispatchAttempts); err != nil {
		if r.pg.IsNoRows(err) {
			return "job not found", nil
		}
		return "", r.mapJobLeaseReadError(err, "read job claim rejection reason")
	}

	return fmt.Sprintf("job status is %s with dispatch attempts %d", jobStatus, dispatchAttempts), nil
}

func (r *Repository) mapJobLeaseReadError(err error, operation string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(grpccodes.DeadlineExceeded, err.Error())
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(grpccodes.Canceled, err.Error())
	}
	if r.pg.IsNoRows(err) {
		return status.Errorf(grpccodes.NotFound, "%s: job not found", operation)
	}
	if r.pg.IsInvalidTextRepresentation(err) {
		return status.Errorf(grpccodes.InvalidArgument, "%s: invalid job ID: %v", operation, err)
	}

	return status.Errorf(grpccodes.Internal, "%s: %v", operation, err)
}

func (r *Repository) mapJobLeaseWriteError(err error, operation string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(grpccodes.DeadlineExceeded, err.Error())
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(grpccodes.Canceled, err.Error())
	}
	if r.pg.IsInvalidTextRepresentation(err) {
		return status.Errorf(grpccodes.InvalidArgument, "%s: invalid job ID: %v", operation, err)
	}

	return status.Errorf(grpccodes.Internal, "%s: %v", operation, err)
}

func leaseSeconds(d time.Duration) int64 {
	if d <= 0 {
		return 1
	}

	seconds := int64(d.Seconds())
	if seconds < 1 {
		return 1
	}

	return seconds
}

func truncateJobError(message string) string {
	if len(message) <= maxJobErrorMessageLength {
		return message
	}

	return message[:maxJobErrorMessageLength]
}
