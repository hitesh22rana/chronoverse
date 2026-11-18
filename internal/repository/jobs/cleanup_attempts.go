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
//
//nolint:gocyclo // Authority, transition and audit must commit under one retained-row lock.
func (r *Repository) UpdateContainerCleanup(
	ctx context.Context,
	operation string,
	identity jobsmodel.CleanupIdentity,
	generation int64,
	token, errorCode string,
	interrupted bool,
	redrive jobsmodel.CleanupRedrive,
) (*jobsmodel.CleanupAttempt, error) {
	if identityErr := validateCleanupIdentity(identity); identityErr != nil {
		return nil, identityErr
	}
	if operation == jobsmodel.CleanupOperationRedrive {
		if auditErr := redrive.Validate(); auditErr != nil {
			return nil, status.Error(codes.InvalidArgument, auditErr.Error())
		}
	}

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return nil, mapBeginTxError(err, "begin cleanup attempt")
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)
	key := cleanupEventKey(identity.JobID, identity.ContainerID, identity.RuntimeInstance)
	var result cleanupAttempt
	var storedToken sql.NullString
	var lease, next sql.NullTime
	var now time.Time
	err = tx.QueryRow(ctx, `
		SELECT
			cleanup_state,
			cleanup_generation,
			cleanup_attempts,
			cleanup_token::text,
			cleanup_lease_until,
			cleanup_next_attempt_at,
			clock_timestamp() AT TIME ZONE 'utc'
		FROM
			outbox_events
		WHERE
			topic = 'workflows'
			AND event_key = $1
			AND cleanup_state IS NOT NULL
			AND payload -> 'ContainerCleanup' ->> 'IntentID' = $2
		FOR UPDATE
	`, key, identity.IntentID).
		Scan(&result.State, &result.Generation, &result.Attempts, &storedToken, &lease, &next, &now)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, r.mapJobLeaseReadError(err, "read cleanup attempt")
	}
	missing := errors.Is(err, pgx.ErrNoRows)
	var completed bool
	if receiptErr := tx.QueryRow(
		ctx,
		`
			SELECT
				EXISTS (
					SELECT
						1
					FROM
						container_cleanup_receipts
					WHERE
						job_id = $1
						AND container_id = $2
						AND runtime_instance = $3
						AND intent_id = $4
				)
		`,
		identity.JobID,
		identity.ContainerID,
		identity.RuntimeInstance,
		identity.IntentID,
	).Scan(&completed); receiptErr != nil {
		return nil, r.mapJobLeaseReadError(receiptErr, "read cleanup receipt")
	}
	if completed {
		return &jobsmodel.CleanupAttempt{State: jobsmodel.CleanupStateCompleted, Generation: result.Generation, Attempts: result.Attempts}, nil
	}
	if missing {
		return nil, status.Error(codes.Unavailable, "durable cleanup intent is missing")
	}
	if generation != result.Generation {
		result.State = jobsmodel.CleanupStateStale
		return &result.CleanupAttempt, nil
	}
	if result.State == jobsmodel.CleanupStateQuarantined && operation != jobsmodel.CleanupOperationRedrive {
		return &result.CleanupAttempt, nil
	}
	result.storedToken = storedToken
	result.lease = lease
	result.next = next
	if transitionErr := result.transition(operation, now, token, interrupted); transitionErr != nil {
		return nil, transitionErr
	}
	if operation == jobsmodel.CleanupOperationRedrive {
		_, auditErr := tx.Exec(ctx, `
INSERT INTO container_cleanup_redrives(
 event_key,job_id,container_id,runtime_instance,intent_id,
 expected_generation,generation,actor,reason
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			key, identity.JobID, identity.ContainerID, identity.RuntimeInstance, identity.IntentID,
			generation, result.Generation, redrive.Actor, redrive.Reason)
		if auditErr != nil {
			return nil, r.mapJobLeaseWriteError(auditErr, "audit cleanup redrive")
		}
	}
	lease = result.lease
	next = result.next
	// Duplicate reports preserve the original failure timestamp and retry window.
	_, err = tx.Exec(ctx, `
		UPDATE outbox_events
		SET
			cleanup_state = $2,
			cleanup_generation = $3,
			cleanup_attempts = $4,
			cleanup_token = NULLIF($5, '')::uuid,
			cleanup_lease_until = $6,
			cleanup_next_attempt_at = $7,
			cleanup_failed_at = CASE
				WHEN $8 = $11 THEN $10
				WHEN $8 = $12 THEN NULL
				ELSE cleanup_failed_at
			END,
			cleanup_error_code = CASE
				WHEN $8 = $11 THEN $9
				WHEN $8 = $12 THEN NULL
				ELSE cleanup_error_code
			END,
			payload = jsonb_set(
				payload,
				'{CleanupGeneration}',
				to_jsonb($3::bigint)
			),
			status = CASE
				WHEN $8 = $12 THEN 'PENDING'::OUTBOX_STATUS
				ELSE status
			END,
			attempts = CASE
				WHEN $8 = $12 THEN 0
				ELSE attempts
			END,
			next_attempt_at = CASE
				WHEN $8 = $12 THEN $10
				ELSE next_attempt_at
			END,
			cleanup_replay_round = CASE
				WHEN $8 = $12 THEN 0
				ELSE cleanup_replay_round
			END,
			cleanup_replay_due_at = CASE
				WHEN $8 = $12 THEN NULL
				ELSE cleanup_replay_due_at
			END,
			published_at = CASE
				WHEN $8 = $12 THEN NULL
				ELSE published_at
			END,
			locked_at = CASE
				WHEN $8 = $12 THEN NULL
				ELSE locked_at
			END,
			locked_by = CASE
				WHEN $8 = $12 THEN NULL
				ELSE locked_by
			END
		WHERE
			topic = 'workflows'
			AND event_key = $1
			AND (
				$8 <> $11
				OR cleanup_state = $13
			)
	`,
		key, result.State, result.Generation, result.Attempts, result.Token, lease, next, operation, errorCode, now,
		jobsmodel.CleanupOperationFail, jobsmodel.CleanupOperationRedrive, jobsmodel.CleanupStateProcessing,
	)
	if err != nil {
		return nil, r.mapJobLeaseWriteError(err, "update cleanup attempt")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, r.mapJobLeaseWriteError(err, "commit cleanup attempt")
	}
	// Only a successful acquisition grants execution authority.
	if operation != jobsmodel.CleanupOperationBegin {
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
	case jobsmodel.CleanupOperationBegin:
		return a.begin(now)
	case jobsmodel.CleanupOperationFail:
		return a.fail(now, token, interrupted)
	case jobsmodel.CleanupOperationRedrive:
		if a.lease.Valid && a.lease.Time.After(now) {
			return status.Error(codes.Aborted, "cleanup attempt is still active")
		}
		a.State = jobsmodel.CleanupStateReady
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
	// Keep cleanupMaxAttempts aligned with the outbox_events.cleanup_attempts CHECK constraint.
	cleanupMaxAttempts  = 3
	cleanupAttemptLease = 4 * time.Minute
	cleanupRetryBackoff = 30 * time.Second
)

func (a *cleanupAttempt) begin(now time.Time) error {
	if a.lease.Valid && a.lease.Time.After(now) || a.next.Valid && a.next.Time.After(now) {
		return status.Error(codes.Aborted, "cleanup attempt is leased or waiting for retry")
	}
	if a.Attempts >= cleanupMaxAttempts {
		a.State = jobsmodel.CleanupStateQuarantined
		a.lease = sql.NullTime{}
		return nil
	}
	a.State = jobsmodel.CleanupStateProcessing
	a.Attempts++
	a.Token = uuid.NewString()
	a.lease = sql.NullTime{Time: now.Add(cleanupAttemptLease), Valid: true}
	return nil
}

func (a *cleanupAttempt) fail(now time.Time, token string, interrupted bool) error {
	if !a.storedToken.Valid || token != a.storedToken.String {
		return status.Error(codes.Aborted, "cleanup attempt ownership changed")
	}
	if a.State != jobsmodel.CleanupStateProcessing {
		a.Token = a.storedToken.String
		return nil
	}
	switch {
	case interrupted:
		a.Attempts--
		a.State = jobsmodel.CleanupStateReady
		a.next = sql.NullTime{}
	case a.Attempts >= cleanupMaxAttempts:
		a.State = jobsmodel.CleanupStateQuarantined
	default:
		a.State = jobsmodel.CleanupStateRetry
		a.next = sql.NullTime{Time: now.Add(time.Duration(a.Attempts) * cleanupRetryBackoff), Valid: true}
	}
	// Retain the token so a response-lost report cannot spend or refund the budget twice.
	a.Token = a.storedToken.String
	a.lease = sql.NullTime{}
	return nil
}
