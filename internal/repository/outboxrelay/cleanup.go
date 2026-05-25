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
