package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

// UpdateContainerCleanup serializes execution, failure reporting and explicit redrive.
// Publication attempts remain separate from this three-attempt execution budget.
func (r *Repository) UpdateContainerCleanup(ctx context.Context, operation, jobID, containerID string, generation int64, token, errorCode string, interrupted bool) (*jobsmodel.CleanupAttempt, error) {
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return nil, mapBeginTxError(err, "begin cleanup attempt")
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)
	key := "job:" + jobID + ":container:" + containerID + ":cleanup"
	var result cleanupAttempt
	var storedToken sql.NullString
	var lease, next sql.NullTime
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT cleanup_state,cleanup_generation,cleanup_attempts,cleanup_token::text,
 cleanup_lease_until,cleanup_next_attempt_at,clock_timestamp() AT TIME ZONE 'utc'
 FROM outbox_events WHERE topic='workflows' AND event_key=$1 AND cleanup_state IS NOT NULL FOR UPDATE`, key).
		Scan(&result.State, &result.Generation, &result.Attempts, &storedToken, &lease, &next, &now)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, r.mapJobLeaseReadError(err, "read cleanup attempt")
	}
	missing := errors.Is(err, pgx.ErrNoRows)
	var completed bool
	if receiptErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM container_cleanup_receipts WHERE job_id=$1 AND container_id=$2)`, jobID, containerID).Scan(&completed); receiptErr != nil {
		return nil, r.mapJobLeaseReadError(receiptErr, "read cleanup receipt")
	}
	if completed {
		return &jobsmodel.CleanupAttempt{State: "COMPLETED", Generation: result.Generation, Attempts: result.Attempts}, nil
	}
	if missing {
		return nil, status.Error(codes.Unavailable, "durable cleanup intent is missing")
	}
	if generation != result.Generation {
		result.State = "STALE"
		return &result.CleanupAttempt, nil
	}
	if result.State == cleanupQuarantined && operation != "redrive" {
		return &result.CleanupAttempt, nil
	}
	result.storedToken = storedToken
	result.lease = lease
	result.next = next
	if transitionErr := result.transition(operation, now, token, interrupted); transitionErr != nil {
		return nil, transitionErr
	}
	lease = result.lease
	next = result.next
	// Duplicate reports preserve the original failure timestamp and retry window.
	_, err = tx.Exec(ctx, `UPDATE outbox_events SET cleanup_state=$2,cleanup_generation=$3,cleanup_attempts=$4,
 cleanup_token=NULLIF($5,'')::uuid,cleanup_lease_until=$6,cleanup_next_attempt_at=$7,
 cleanup_failed_at=CASE WHEN $8='fail' THEN $10 WHEN $8='redrive' THEN NULL ELSE cleanup_failed_at END,
 cleanup_error_code=CASE WHEN $8='fail' THEN $9 WHEN $8='redrive' THEN NULL ELSE cleanup_error_code END,
 payload=jsonb_set(payload,'{CleanupGeneration}',to_jsonb($3::bigint)),
 status=CASE WHEN $8='redrive' THEN 'PENDING'::OUTBOX_STATUS ELSE status END,
 attempts=CASE WHEN $8='redrive' THEN 0 ELSE attempts END,
 next_attempt_at=CASE WHEN $8='redrive' THEN $10 ELSE next_attempt_at END,
 published_at=CASE WHEN $8='redrive' THEN NULL ELSE published_at END,
 locked_at=CASE WHEN $8='redrive' THEN NULL ELSE locked_at END,
 locked_by=CASE WHEN $8='redrive' THEN NULL ELSE locked_by END
 WHERE topic='workflows' AND event_key=$1 AND ($8 <> 'fail' OR cleanup_state='PROCESSING')`,
		key, result.State, result.Generation, result.Attempts, result.Token, lease, next, operation, errorCode, now)
	if err != nil {
		return nil, r.mapJobLeaseWriteError(err, "update cleanup attempt")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, r.mapJobLeaseWriteError(err, "commit cleanup attempt")
	}
	// Only a successful acquisition grants execution authority.
	if operation != "begin" {
		result.Token = ""
	}
	return &result.CleanupAttempt, nil
}

type cleanupAttempt struct {
	jobsmodel.CleanupAttempt
	storedToken sql.NullString
	lease       sql.NullTime
	next        sql.NullTime
}

func (a *cleanupAttempt) transition(operation string, now time.Time, token string, interrupted bool) error {
	switch operation {
	case "begin":
		return a.begin(now)
	case "fail":
		return a.fail(now, token, interrupted)
	case "redrive":
		if a.lease.Valid && a.lease.Time.After(now) {
			return status.Error(codes.Aborted, "cleanup attempt is still active")
		}
		a.State = "READY"
		a.Generation++
		a.Attempts = 0
		a.next = sql.NullTime{}
		a.lease = sql.NullTime{}
		return nil
	default:
		return status.Error(codes.InvalidArgument, "unknown cleanup operation")
	}
}

const (
	cleanupQuarantined = "QUARANTINED"
	cleanupProcessing  = "PROCESSING"
)

func (a *cleanupAttempt) begin(now time.Time) error {
	if a.lease.Valid && a.lease.Time.After(now) || a.next.Valid && a.next.Time.After(now) {
		return status.Error(codes.Aborted, "cleanup attempt is leased or waiting for retry")
	}
	if a.Attempts >= 3 {
		a.State = cleanupQuarantined
		a.lease = sql.NullTime{}
		return nil
	}
	a.State = cleanupProcessing
	a.Attempts++
	a.Token = uuid.NewString()
	a.lease = sql.NullTime{Time: now.Add(4 * time.Minute), Valid: true}
	return nil
}

func (a *cleanupAttempt) fail(now time.Time, token string, interrupted bool) error {
	if !a.storedToken.Valid || token != a.storedToken.String {
		return status.Error(codes.Aborted, "cleanup attempt ownership changed")
	}
	if a.State != cleanupProcessing {
		a.Token = a.storedToken.String
		return nil
	}
	switch {
	case interrupted:
		a.Attempts--
		a.State = "READY"
		a.next = sql.NullTime{}
	case a.Attempts >= 3:
		a.State = cleanupQuarantined
	default:
		a.State = "RETRY"
		a.next = sql.NullTime{Time: now.Add(time.Duration(a.Attempts) * 30 * time.Second), Valid: true}
	}
	// Retain the token so a response-lost report cannot spend or refund the budget twice.
	a.Token = a.storedToken.String
	a.lease = sql.NullTime{}
	return nil
}
