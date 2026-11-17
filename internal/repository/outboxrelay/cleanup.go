package outboxrelay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

const (
	cleanupSQLStateQueryCanceled   = "57014"
	cleanupSQLStateUndefinedTable  = "42P01"
	cleanupSQLStateUndefinedColumn = "42703"
)

// CleanupPublishedEvents bounds candidate/proof work, returning only confirmed commits.
func (r *Repository) CleanupPublishedEvents(ctx context.Context, retention time.Duration, batchSize int) (int64, error) {
	if retention <= 0 || batchSize <= 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, status.FromContextError(err).Err()
	}
	if _, ok := ctx.Deadline(); !ok {
		return 0, status.Error(codes.InvalidArgument, "published outbox cleanup requires a caller deadline")
	}
	reservationCtx, cancel := cleanupOperationContext(ctx, 4)
	firstCleanup, reserved, err := r.reserveCleanupTurn(reservationCtx)
	cancel()
	if err != nil || !reserved {
		return 0, cleanupOperationError(ctx, err)
	}

	cutoff := time.Now().UTC().Add(-retention)
	var deleted int64
	charged := 0
	excluded := []string{}
	failed := map[bool]bool{}
	var failures []error
	for operation, cleanup := range []bool{firstCleanup, !firstCleanup, firstCleanup} {
		allowance := batchSize - charged
		if operation == 0 {
			allowance = batchSize/2 + batchSize%2
		}
		if allowance == 0 || failed[cleanup] {
			continue
		}
		operationCtx, stop := cleanupOperationContext(ctx, 3-operation)
		if operationCtx.Err() != nil {
			failures = append(failures, status.FromContextError(operationCtx.Err()).Err())
			stop()
			break
		}
		ids, removed, laneErr := r.cleanupPublishedLane(operationCtx, cutoff, allowance, cleanup, excluded)
		stop()

		if laneErr != nil {
			charged += allowance
			failed[cleanup] = true
			failures = append(failures, laneErr)
			logCleanupLane(ctx, cleanup, allowance, ids, 0, laneErr)
			continue
		}
		charged += len(ids)
		excluded = append(excluded, ids...)
		deleted += removed
		logCleanupLane(ctx, cleanup, allowance, ids, removed, nil)
	}
	return deleted, cleanupFailures(ctx, failures)
}

func logCleanupLane(ctx context.Context, cleanup bool, allowance int, ids []string, removed int64, err error) {
	lane := "ordinary"
	if cleanup {
		lane = "cleanup"
	}
	fields := []zap.Field{zap.String("lane", lane), zap.Int("allowance", allowance)}
	if err != nil {
		loggerpkg.FromContext(ctx).Warn("published outbox cleanup lane failed", append(fields,
			zap.Int("charged", allowance), zap.Int("selected", len(ids)), zap.Bool("selected_known", ids != nil),
			zap.Int64("deleted", 0), zap.Bool("uncertain", ids != nil && status.Code(err) != codes.Internal), zap.Error(err))...)
		return
	}
	mismatched := int64(0)
	if cleanup {
		mismatched = int64(len(ids)) - removed
	}
	fields = append(fields, zap.Int("selected", len(ids)), zap.Int("charged", len(ids)),
		zap.Int64("deleted", removed), zap.Int64("proof_mismatches", mismatched))
	if mismatched > 0 {
		loggerpkg.FromContext(ctx).Warn("published cleanup candidates lack exact receipts", fields...)
	} else {
		loggerpkg.FromContext(ctx).Debug("pruned published outbox lane", fields...)
	}
}

func cleanupFailures(ctx context.Context, failures []error) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	if len(failures) == 0 {
		return nil
	}
	code := codes.Internal
	for _, failure := range failures {
		if status.Code(failure) == codes.Unavailable {
			code = codes.Unavailable
			break
		}
		if status.Code(failure) == codes.DeadlineExceeded {
			code = codes.DeadlineExceeded
		}
	}
	return status.Errorf(code, "published outbox cleanup: %v", errors.Join(failures...))
}

func cleanupOperationContext(ctx context.Context, remaining int) (context.Context, context.CancelFunc) {
	deadline, _ := ctx.Deadline() // The public entry point requires a deadline.
	return context.WithTimeout(ctx, time.Until(deadline)/time.Duration(remaining))
}

func cleanupOperationError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return status.Errorf(codes.Internal, "outbox cleanup commit rolled back: %v", err)
	}
	var databaseErr *pgconn.PgError
	if errors.As(err, &databaseErr) {
		if databaseErr.Code == cleanupSQLStateQueryCanceled {
			return status.Errorf(codes.DeadlineExceeded, "outbox cleanup statement canceled: %v", err)
		}
		if strings.HasPrefix(databaseErr.Code, "08") || databaseErr.Severity == "FATAL" {
			return status.Errorf(codes.Unavailable, "outbox cleanup database unavailable: %v", err)
		}
		if databaseErr.Code == cleanupSQLStateUndefinedTable || databaseErr.Code == cleanupSQLStateUndefinedColumn {
			return status.Errorf(codes.Internal, "outbox cleanup schema is incomplete; initialize from current migrations (existing development databases may need recreation): %v", err)
		}
		return status.Errorf(codes.Internal, "outbox cleanup SQL: %v", err)
	}
	return status.Errorf(codes.Unavailable, "outbox cleanup transport: %v", err)
}

func (r *Repository) reserveCleanupTurn(ctx context.Context) (nextCleanup, reserved bool, resultErr error) {
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return false, false, cleanupOperationError(ctx, err)
	}
	defer func() {
		//nolint:errcheck // Best-effort cleanup; commit/query errors remain authoritative.
		_ = tx.Rollback(ctx)
	}()
	var cleanup bool
	err = tx.QueryRow(ctx, `SELECT next_cleanup FROM outbox_cleanup_turn WHERE id=TRUE FOR UPDATE SKIP LOCKED`).Scan(&cleanup)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbox_cleanup_turn WHERE id=TRUE)`).Scan(&exists); err != nil {
			return false, false, cleanupOperationError(ctx, err)
		}
		if !exists {
			return false, false, status.Error(codes.Internal, "outbox cleanup maintenance turn is missing; reinitialize the database with the current migrations")
		}
		loggerpkg.FromContext(ctx).Debug("outbox cleanup maintenance turn busy")
		return false, false, nil
	}
	if err != nil {
		return false, false, cleanupOperationError(ctx, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE outbox_cleanup_turn SET next_cleanup=NOT next_cleanup WHERE id=TRUE`); err != nil {
		return false, false, cleanupOperationError(ctx, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, false, cleanupOperationError(ctx, err)
	}
	return cleanup, true, nil
}

func (r *Repository) cleanupPublishedLane(
	ctx context.Context,
	cutoff time.Time,
	allowance int,
	cleanup bool,
	excluded []string,
) (selected []string, removed int64, resultErr error) {
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return nil, 0, cleanupOperationError(ctx, err)
	}
	defer func() {
		//nolint:errcheck // Best-effort cleanup; commit/query errors remain authoritative.
		_ = tx.Rollback(ctx)
	}()
	// Bitmap scans sort the whole eligible lane before LIMIT under correlated-state estimates.
	// Keep the setting transaction-local so publication and other queries are unaffected.
	if _, err = tx.Exec(ctx, "SET LOCAL enable_bitmapscan=off"); err != nil {
		return nil, 0, cleanupOperationError(ctx, err)
	}
	predicate := "payload->>'Action' IS DISTINCT FROM 'CONTAINER_CLEANUP'"
	proof := "TRUE"
	if cleanup {
		predicate = "cleanup_state='COMPLETED' AND payload->>'Action'='CONTAINER_CLEANUP'"
		// A scalar PK lookup prevents a hashed EXISTS from reading all retained receipts.
		proof = `(SELECT receipt.intent_id::text
			FROM container_cleanup_receipts receipt
			WHERE receipt.job_id=CASE WHEN pg_input_is_valid(events.payload->>'JobID','uuid')
				THEN (events.payload->>'JobID')::uuid END
			AND receipt.container_id=events.payload->'ContainerCleanup'->>'ContainerID'
			AND receipt.runtime_instance=events.payload->'ContainerCleanup'->>'RuntimeInstance'
		)=events.payload->'ContainerCleanup'->>'IntentID'`
	}
	query := fmt.Sprintf(`
		WITH candidates AS MATERIALIZED (
			SELECT id FROM outbox_events
			WHERE status='PUBLISHED' AND published_at<$1 AND %s
				AND NOT (id=ANY($3::uuid[]))
			ORDER BY published_at,id
			LIMIT $2 FOR UPDATE SKIP LOCKED
		), removed AS (
			DELETE FROM outbox_events events USING candidates candidate
			WHERE events.id=candidate.id AND %s
			RETURNING events.id
		)
		SELECT COALESCE(array_agg(id::text),'{}'::text[]), (SELECT count(*) FROM removed)
		FROM candidates`, predicate, proof)
	var ids []string
	var deleted int64
	if err = tx.QueryRow(ctx, query, cutoff, allowance, excluded).Scan(&ids, &deleted); err != nil {
		return nil, 0, cleanupOperationError(ctx, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return ids, 0, cleanupOperationError(ctx, err)
	}
	return ids, deleted, nil
}

// CleanupCommandIdempotencyKeys physically deletes logically expired command
// records in a bounded, non-blocking batch. Expired-key replacement remains
// independent of this maintenance operation.
func (r *Repository) CleanupCommandIdempotencyKeys(ctx context.Context, batchSize int) (int64, error) {
	if batchSize <= 0 {
		return 0, nil
	}

	query := fmt.Sprintf(`
		WITH expired AS (
			SELECT scope, operation, idempotency_key
			FROM %s
			WHERE expires_at IS NOT NULL
				AND expires_at <= (clock_timestamp() AT TIME ZONE 'utc')
			ORDER BY expires_at, scope, operation, idempotency_key
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		DELETE FROM %s AS ledger
		USING expired
		WHERE ledger.scope = expired.scope
			AND ledger.operation = expired.operation
			AND ledger.idempotency_key = expired.idempotency_key;
	`, postgres.TableCommandIdempotencyKeys, postgres.TableCommandIdempotencyKeys)

	tag, err := r.pg.Exec(ctx, query, batchSize)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "failed to cleanup command idempotency keys: %v", err)
	}

	return tag.RowsAffected(), nil
}
