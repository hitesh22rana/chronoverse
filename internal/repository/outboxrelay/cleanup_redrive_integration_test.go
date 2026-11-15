//nolint:testpackage // Uses shared PostgreSQL and Kafka relay fixtures.
package outboxrelay

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationRelayRepublishesUnfinishedCleanupWithoutResettingBudget(t *testing.T) {
	pg := testkit.Postgres(t)
	topic := "workflows"
	past := time.Now().Add(-10 * time.Minute)
	for _, state := range []string{"READY", "RETRY", "PROCESSING", "QUARANTINED", "COMPLETED"} {
		seedOutboxRow(t.Context(), t, pg, topic, state, statusPublished, &past)
		if _, err := pg.Exec(t.Context(), `UPDATE outbox_events SET kafka_key=event_key,cleanup_state=$2,cleanup_attempts=2,cleanup_generation=4 WHERE topic=$1 AND event_key=$2`, topic, state); err != nil {
			t.Fatal(err)
		}
	}
	seedDeferredCleanup(t, topic, past)
	repo := New(&Config{BatchSize: 20, MaxAttempts: 3, WorkerID: "redrive-test"}, pg, testkit.Kafka(t))
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if count, err := repo.PublishTopic(canceled, topic, zap.NewNop()); status.Code(err) != codes.Internal || count != 0 {
		t.Fatalf("failed sweep granted publication: %d/%v", count, err)
	}
	count, err := repo.PublishTopic(t.Context(), topic, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("redriven=%d want 3 unfinished expired deliveries", count)
	}
	for _, state := range []string{"READY", "RETRY", "PROCESSING"} {
		var attempts, publicationAttempts int
		var generation int64
		var published time.Time
		if err := pg.QueryRow(t.Context(), `SELECT cleanup_attempts,cleanup_generation,attempts,published_at FROM outbox_events WHERE topic=$1 AND event_key=$2`,
			topic, state).Scan(&attempts, &generation, &publicationAttempts, &published); err != nil {
			t.Fatal(err)
		}
		if attempts != 2 || generation != 4 || publicationAttempts != 1 || published.Before(time.Now().Add(-time.Minute)) {
			t.Fatalf("redrive reset execution authority: %s %d/%d/%d/%v", state, attempts, generation, publicationAttempts, published)
		}
	}
	if count, err := repo.PublishTopic(t.Context(), topic, zap.NewNop()); err != nil || count != 0 {
		t.Fatalf("immediate duplicate publication %d/%v", count, err)
	}
}

func seedDeferredCleanup(t *testing.T, topic string, past time.Time) {
	t.Helper()
	pg := testkit.Postgres(t)
	// An active or delayed attempt must not be redriven.
	for _, name := range []string{"leased", "backoff", "recent"} {
		seedOutboxRow(t.Context(), t, pg, topic, name, statusPublished, &past)
		if _, err := pg.Exec(t.Context(), `UPDATE outbox_events SET kafka_key=event_key,cleanup_state='PROCESSING',
 cleanup_lease_until=CASE WHEN event_key='leased' THEN now() AT TIME ZONE 'utc'+interval '1 minute' END,
 cleanup_next_attempt_at=CASE WHEN event_key='backoff' THEN now() AT TIME ZONE 'utc'+interval '1 minute' END,
 published_at=CASE WHEN event_key='recent' THEN now() AT TIME ZONE 'utc' ELSE published_at END WHERE topic=$1 AND event_key=$2`, topic, name); err != nil {
			t.Fatal(err)
		}
	}
}
