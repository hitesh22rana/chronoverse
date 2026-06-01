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
	query := fmt.Sprintf(`
        UPDATE %s AS j
        SET status = 'RUNNING',
            attempts = attempts + 1,
            lease_token = $3,
            leased_by = $4,
            lease_expires_at = (now() AT TIME ZONE 'utc') + ($5::int * interval '1 second'),
            last_heartbeat_at = now() AT TIME ZONE 'utc',
            started_at = now() AT TIME ZONE 'utc',
            completed_at = NULL,
            failure_kind = NULL,
            last_error_code = NULL,
            last_error_message = NULL
        WHERE j.id = $1
            AND j.workflow_id = $2
            AND j.status = 'QUEUED'
            AND j.dispatch_attempts = $6
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
        RETURNING id, workflow_id, user_id, trigger, scheduled_at, dispatch_attempts, attempts, lease_token;
    `, postgres.TableJobs, postgres.TableJobs, postgres.TableJobs)

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
	).Scan(
		&claimed.ID,
		&claimed.WorkflowID,
		&claimed.UserID,
		&claimed.Trigger,
		&claimed.ScheduledAt,
		&claimed.DispatchAttempts,
		&claimed.Attempts,
		&claimed.LeaseToken,
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

	reason, err = r.jobClaimRejectionReason(ctx, jobID)
	if err != nil {
		return nil, false, "", err
	}

	return nil, false, reason, nil
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
func (r *Repository) AttachJobContainer(ctx context.Context, jobID, leaseToken, containerID string) (err error) {
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
        WHERE id = $1 AND lease_token = $2 AND status = 'RUNNING';
    `, postgres.TableJobs)

	return r.execLeaseUpdate(ctx, query, "attach job container", jobID, leaseToken, containerID)
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

	if err := tx.Commit(ctx); err != nil {
		return r.mapJobLeaseWriteError(err, "commit complete job transaction")
	}

	return nil
}

// FailJob marks a running claimed job as failed.
func (r *Repository) FailJob(ctx context.Context, jobID, leaseToken, failureKind, errorCode, errorMessage string) (err error) {
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
            last_error_message = $5
        WHERE id = $1 AND lease_token = $2 AND status = 'RUNNING'
        RETURNING id, workflow_id, user_id, started_at, completed_at;
    `, postgres.TableJobs)

	truncatedMessage := truncateJobError(errorMessage)
	snapshot, err := r.scanTerminalJobSnapshot(ctx, tx, query, "fail job", jobID, leaseToken, failureKind, errorCode, truncatedMessage)
	if err != nil {
		return err
	}

	if err := r.insertTerminalJobOutboxEvents(ctx, tx, snapshot, workflowsmodel.ActionJobFailed, failureKind, errorCode, truncatedMessage); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return r.mapJobLeaseWriteError(err, "commit fail job transaction")
	}

	return nil
}

// CancelClaimedJob marks a running claimed job as canceled.
func (r *Repository) CancelClaimedJob(ctx context.Context, jobID, leaseToken string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.CancelClaimedJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
        UPDATE %s
        SET status = 'CANCELED',
            completed_at = now() AT TIME ZONE 'utc',
            lease_token = NULL,
            leased_by = NULL,
            lease_expires_at = NULL,
            last_heartbeat_at = NULL
        WHERE id = $1 AND lease_token = $2 AND status = 'RUNNING';
    `, postgres.TableJobs)

	return r.execLeaseUpdate(ctx, query, "cancel claimed job", jobID, leaseToken)
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

	query := fmt.Sprintf(`
        UPDATE %s
        SET status = 'PENDING',
            queued_at = NULL,
            container_id = NULL,
            started_at = NULL,
            completed_at = NULL,
            lease_token = NULL,
            leased_by = NULL,
            lease_expires_at = NULL,
            last_heartbeat_at = NULL,
            next_attempt_at = $3,
            failure_kind = $4,
            last_error_code = $5,
            last_error_message = $6
        WHERE id = $1 AND lease_token = $2 AND status = 'RUNNING';
    `, postgres.TableJobs)

	return r.execLeaseUpdate(
		ctx,
		query,
		"release job for retry",
		jobID,
		leaseToken,
		nextAttemptAtTime,
		jobsmodel.FailureKindSystem.ToString(),
		errorCode,
		truncateJobError(errorMessage),
	)
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
	query := fmt.Sprintf(`
        WITH expired AS (
            SELECT j.id
            FROM %s AS j
            WHERE j.status = 'RUNNING'
                AND j.lease_token IS NOT NULL
                AND j.lease_expires_at IS NOT NULL
                AND j.lease_expires_at < (now() AT TIME ZONE 'utc')
            ORDER BY j.lease_expires_at ASC, j.id ASC
            FOR UPDATE SKIP LOCKED
            LIMIT $1
        )
        UPDATE %s AS j
        SET lease_token = $2,
            leased_by = $3,
            lease_expires_at = (now() AT TIME ZONE 'utc') + ($4::int * interval '1 second'),
            last_heartbeat_at = now() AT TIME ZONE 'utc'
        FROM expired, %s AS w
        WHERE j.id = expired.id
            AND w.id = j.workflow_id
        RETURNING j.id, j.workflow_id, j.user_id, j.container_id, j.lease_token, j.leased_by, j.trigger, j.scheduled_at, j.attempts, w.log_retention;
    `, postgres.TableJobs, postgres.TableJobs, postgres.TableWorkflows)

	rows, err := r.pg.Query(ctx, query, batchSize, leaseToken, workerID, leaseSeconds(leaseDuration))
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
