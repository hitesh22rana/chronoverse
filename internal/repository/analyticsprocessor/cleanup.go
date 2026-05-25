package analyticsprocessor

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

// CleanupProcessedEvents deletes old processed-event dedupe rows in a bounded batch.
func (r *Repository) CleanupProcessedEvents(ctx context.Context, retention time.Duration, batchSize int) (int64, error) {
	if retention <= 0 || batchSize <= 0 {
		return 0, nil
	}

	query := fmt.Sprintf(`
		WITH expired AS (
			SELECT consumer, event_key
			FROM %s
			WHERE created_at < $1
			ORDER BY created_at
			LIMIT $2
		)
		DELETE FROM %s events
		USING expired
		WHERE events.consumer = expired.consumer
			AND events.event_key = expired.event_key;
	`, postgres.TableProcessedEvents, postgres.TableProcessedEvents)

	tag, err := r.pg.Exec(ctx, query, time.Now().UTC().Add(-retention), batchSize)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "failed to cleanup processed events: %v", err)
	}

	return tag.RowsAffected(), nil
}
