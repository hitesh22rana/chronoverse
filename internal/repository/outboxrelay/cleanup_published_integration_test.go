//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package outboxrelay

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// Outbox row statuses exercised by the integration assertions.
const (
	statusPending    = "PENDING"
	statusProcessing = "PROCESSING"
	statusPublished  = "PUBLISHED"
	statusFailed     = "FAILED"
	statusDead       = "DEAD"
)

// seedOutboxRow inserts an outbox row with an explicit status and optional
// publish timestamp, bypassing the relay so cleanup sees exact timestamps.
func seedOutboxRow(ctx context.Context, t *testing.T, pg *postgres.Postgres, topic, eventKey, statusValue string, publishedAt *time.Time) {
	t.Helper()

	var published any
	if publishedAt != nil {
		published = publishedAt.UTC()
	}

	if _, err := pg.Exec(ctx, `
		INSERT INTO outbox_events (topic, kafka_key, event_key, payload, status, published_at)
		VALUES ($1, $2, $3, '{"job_id":"job-1"}'::jsonb, $4, $5)`, topic, "wf-cleanup", eventKey, statusValue, published); err != nil {
		t.Fatalf("seed %s row: %v", eventKey, err)
	}
}

// listOutboxKeys returns the event keys still stored for a topic.
func listOutboxKeys(ctx context.Context, t *testing.T, pg *postgres.Postgres, topic string) []string {
	t.Helper()

	rows, err := pg.Query(ctx, `SELECT event_key FROM outbox_events WHERE topic = $1 ORDER BY event_key`, topic)
	if err != nil {
		t.Fatalf("list outbox keys: %v", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scan outbox key: %v", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate outbox keys: %v", err)
	}

	return keys
}

// seedExpiredCommandKey inserts one expired command idempotency key.
func seedExpiredCommandKey(ctx context.Context, t *testing.T, pg *postgres.Postgres, scope string) {
	t.Helper()

	if _, err := pg.Exec(ctx, `
		INSERT INTO command_idempotency_keys
			(scope, operation, idempotency_key, request_hash, status, resource_id, response, created_at, updated_at, completed_at, expires_at)
		VALUES ($1, 'op', 'expired', $2, 'COMPLETED', 'resource-1', '{}',
		        (now() AT TIME ZONE 'utc') - interval '2 hours',
		        now() AT TIME ZONE 'utc',
		        now() AT TIME ZONE 'utc',
		        (now() AT TIME ZONE 'utc') - interval '1 hour')`,
		scope, fmt.Sprintf("%064x", 1),
	); err != nil {
		t.Fatalf("seed expired command key: %v", err)
	}
}

func TestIntegrationCleanupPublishedEventsRespectsRetentionAndBatch(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-cleanup-published-" + t.Name()
	repo := New(&Config{BatchSize: 10, MaxAttempts: 3, RetryBackoff: time.Minute, ProcessingLease: time.Minute, WorkerID: "cleanup-worker"}, pg, nil)

	now := time.Now().UTC()
	old3h := now.Add(-3 * time.Hour)
	old2h := now.Add(-2 * time.Hour)
	old90m := now.Add(-90 * time.Minute)
	recent := now.Add(-time.Minute)
	veryOld := now.Add(-5 * time.Hour)

	seedOutboxRow(ctx, t, pg, topic, "old-3h", statusPublished, &old3h)
	seedOutboxRow(ctx, t, pg, topic, "old-2h", statusPublished, &old2h)
	seedOutboxRow(ctx, t, pg, topic, "old-90m", statusPublished, &old90m)
	seedOutboxRow(ctx, t, pg, topic, "recent", statusPublished, &recent)
	seedOutboxRow(ctx, t, pg, topic, "processing", statusProcessing, nil)
	seedOutboxRow(ctx, t, pg, topic, "pending", statusPending, nil)
	// Non-published rows carrying an old publish timestamp must survive the
	// status filter: only PUBLISHED rows are cleanup candidates.
	seedOutboxRow(ctx, t, pg, topic, "failed-old-timestamp", statusFailed, &veryOld)
	seedOutboxRow(ctx, t, pg, topic, "dead-old-timestamp", statusDead, &veryOld)

	deleted, err := repo.CleanupPublishedEvents(ctx, time.Hour, 2)
	if err != nil {
		t.Fatalf("CleanupPublishedEvents: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted %d rows with batch size 2, want 2 (oldest published first)", deleted)
	}

	want := []string{"dead-old-timestamp", "failed-old-timestamp", "old-90m", "pending", "processing", "recent"}
	if got := listOutboxKeys(ctx, t, pg, topic); !slices.Equal(got, want) {
		t.Fatalf("remaining keys = %v, want %v", got, want)
	}

	deleted, err = repo.CleanupPublishedEvents(ctx, time.Hour, 2)
	if err != nil {
		t.Fatalf("CleanupPublishedEvents (second batch): %v", err)
	}
	if deleted != 1 {
		t.Fatalf("second batch deleted %d rows, want 1 (old-90m)", deleted)
	}

	want = []string{"dead-old-timestamp", "failed-old-timestamp", "pending", "processing", "recent"}
	if got := listOutboxKeys(ctx, t, pg, topic); !slices.Equal(got, want) {
		t.Fatalf("remaining keys after second batch = %v, want %v", got, want)
	}

	deleted, err = repo.CleanupPublishedEvents(ctx, time.Hour, 10)
	if err != nil {
		t.Fatalf("CleanupPublishedEvents (drained): %v", err)
	}
	if deleted != 0 {
		t.Fatalf("drained cleanup deleted %d rows, want 0", deleted)
	}
}

func TestIntegrationCleanupPublishedEventsNoOpForInvalidBounds(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-cleanup-noop-" + t.Name()
	repo := New(&Config{BatchSize: 10, MaxAttempts: 3, RetryBackoff: time.Minute, ProcessingLease: time.Minute, WorkerID: "cleanup-worker"}, pg, nil)

	old := time.Now().UTC().Add(-2 * time.Hour)
	seedOutboxRow(ctx, t, pg, topic, "old", statusPublished, &old)

	cases := []struct {
		name      string
		retention time.Duration
		batchSize int
	}{
		{name: "zero retention", retention: 0, batchSize: 10},
		{name: "negative retention", retention: -time.Hour, batchSize: 10},
		{name: "zero batch", retention: time.Hour, batchSize: 0},
		{name: "negative batch", retention: time.Hour, batchSize: -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deleted, err := repo.CleanupPublishedEvents(ctx, tc.retention, tc.batchSize)
			if err != nil {
				t.Fatalf("CleanupPublishedEvents(%s, %d): %v", tc.retention, tc.batchSize, err)
			}
			if deleted != 0 {
				t.Fatalf("no-op cleanup deleted %d rows, want 0", deleted)
			}
			if got := listOutboxKeys(ctx, t, pg, topic); !slices.Equal(got, []string{"old"}) {
				t.Fatalf("no-op cleanup changed rows: %v", got)
			}
		})
	}

	// The row is a real candidate again once the bounds are valid.
	deleted, err := repo.CleanupPublishedEvents(ctx, time.Hour, 10)
	if err != nil {
		t.Fatalf("CleanupPublishedEvents (valid bounds): %v", err)
	}
	if deleted != 1 {
		t.Fatalf("valid cleanup deleted %d rows, want 1", deleted)
	}
}

func TestIntegrationCleanupDatabaseErrorsSurfaceInternal(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := New(&Config{BatchSize: 10, MaxAttempts: 3, RetryBackoff: time.Minute, ProcessingLease: time.Minute, WorkerID: "cleanup-worker"}, pg, nil)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := repo.CleanupPublishedEvents(canceled, time.Hour, 10); status.Code(err) != codes.Internal {
		t.Fatalf("CleanupPublishedEvents with canceled context = %v, want Internal", err)
	}
	if _, err := repo.CleanupCommandIdempotencyKeys(canceled, 10); status.Code(err) != codes.Internal {
		t.Fatalf("CleanupCommandIdempotencyKeys with canceled context = %v, want Internal", err)
	}
}

func TestIntegrationCleanupCommandIdempotencyKeysNoOpForInvalidBatch(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	scope := "cleanup-noop-" + t.Name()
	repo := New(&Config{BatchSize: 10, MaxAttempts: 3, RetryBackoff: time.Minute, ProcessingLease: time.Minute, WorkerID: "cleanup-worker"}, pg, nil)

	seedExpiredCommandKey(ctx, t, pg, scope)

	for _, batchSize := range []int{0, -1} {
		deleted, err := repo.CleanupCommandIdempotencyKeys(ctx, batchSize)
		if err != nil {
			t.Fatalf("CleanupCommandIdempotencyKeys(%d): %v", batchSize, err)
		}
		if deleted != 0 {
			t.Fatalf("no-op cleanup deleted %d rows with batch size %d, want 0", deleted, batchSize)
		}
	}

	var remaining int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM command_idempotency_keys WHERE scope = $1`, scope).Scan(&remaining); err != nil {
		t.Fatalf("count remaining keys: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining keys = %d, want 1", remaining)
	}

	deleted, err := repo.CleanupCommandIdempotencyKeys(ctx, 10)
	if err != nil {
		t.Fatalf("CleanupCommandIdempotencyKeys (valid batch): %v", err)
	}
	if deleted != 1 {
		t.Fatalf("valid cleanup deleted %d rows, want 1", deleted)
	}
}
