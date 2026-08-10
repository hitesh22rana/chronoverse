package outboxrelay

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

// CleanupPublishedEvents deletes old published outbox events in a bounded batch.
func (r *Repository) CleanupPublishedEvents(ctx context.Context, retention time.Duration, batchSize int) (int64, error) {
	if retention <= 0 || batchSize <= 0 {
		return 0, nil
	}

	query := fmt.Sprintf(`
		WITH expired AS (
			SELECT id
			FROM %s
			WHERE status = 'PUBLISHED'
				AND published_at < $1
			ORDER BY published_at
			LIMIT $2
		)
		DELETE FROM %s events
		USING expired
		WHERE events.id = expired.id;
	`, postgres.TableOutboxEvents, postgres.TableOutboxEvents)

	tag, err := r.pg.Exec(ctx, query, time.Now().UTC().Add(-retention), batchSize)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "failed to cleanup published outbox events: %v", err)
	}

	return tag.RowsAffected(), nil
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
