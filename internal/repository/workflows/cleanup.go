package workflows

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

// CleanupWorkflowIdempotencyKeys deletes expired workflow idempotency keys in a bounded batch.
func (r *Repository) CleanupWorkflowIdempotencyKeys(ctx context.Context, batchSize int) (int64, error) {
	if batchSize <= 0 {
		return 0, nil
	}

	query := fmt.Sprintf(`
		WITH expired AS (
			SELECT user_id, operation, idempotency_key
			FROM %s
			WHERE expires_at < (now() AT TIME ZONE 'utc')
			ORDER BY expires_at
			LIMIT $1
		)
		DELETE FROM %s keys
		USING expired
		WHERE keys.user_id = expired.user_id
			AND keys.operation = expired.operation
			AND keys.idempotency_key = expired.idempotency_key;
	`, postgres.TableWorkflowIdempotencyKeys, postgres.TableWorkflowIdempotencyKeys)

	tag, err := r.pg.Exec(ctx, query, batchSize)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "failed to cleanup workflow idempotency keys: %v", err)
	}

	return tag.RowsAffected(), nil
}
