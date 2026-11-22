package outboxrelay

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

// requeueUnfinishedCleanup repairs lost/expired Kafka delivery without resetting
// the execution budget. Explicit administrative redrive alone creates a generation.
func (r *Repository) requeueUnfinishedCleanup(ctx context.Context, topic string) error {
	if topic != kafka.TopicWorkflows {
		return nil
	}
	_, err := r.pg.Exec(ctx, `
		WITH
			picked AS (
				SELECT
					id
				FROM
					outbox_events
				WHERE
					topic = $1
					AND status = 'PUBLISHED'
					AND cleanup_state IN ('READY', 'RETRY', 'PROCESSING')
					AND cleanup_replay_due_at <= (clock_timestamp() AT TIME ZONE 'utc')
					AND COALESCE(
						cleanup_lease_until,
						'-infinity'::timestamp
					) <= (clock_timestamp() AT TIME ZONE 'utc')
					AND COALESCE(
						cleanup_next_attempt_at,
						'-infinity'::timestamp
					) <= (clock_timestamp() AT TIME ZONE 'utc')
				ORDER BY
					cleanup_replay_due_at,
					id
				FOR UPDATE
					SKIP LOCKED
				LIMIT
					$2
			)
		UPDATE outbox_events e
		SET
			status = 'PENDING',
			attempts = 0,
			next_attempt_at = clock_timestamp() AT TIME ZONE 'utc',
			cleanup_replay_round = LEAST(63, cleanup_replay_round + 1),
			cleanup_replay_due_at = NULL,
			published_at = NULL,
			locked_at = NULL,
			locked_by = NULL
		FROM
			picked
		WHERE
			e.id = picked.id
	`, topic, r.cfg.BatchSize)
	if err != nil {
		return status.Errorf(codes.Internal, "requeue unfinished cleanup: %v", err)
	}
	return nil
}
