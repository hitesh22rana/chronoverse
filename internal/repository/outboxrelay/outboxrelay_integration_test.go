//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package outboxrelay

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres(), testkit.WithKafka())
}

func TestIntegrationPublishTopic(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(&Config{
		BatchSize:       100,
		MaxAttempts:     3,
		RetryBackoff:    time.Second,
		ProcessingLease: 30 * time.Second,
		WorkerID:        "integration-worker",
	}, pg, testkit.Kafka(t))

	// Insert an outbox event with the production helper.
	tx, err := pg.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if insertErr := outbox.InsertTx(ctx, tx, &outbox.Event{
		Topic:    kafka.TopicJobs,
		KafkaKey: "workflow-1",
		EventKey: "event-" + t.Name(),
		Payload:  map[string]any{"job_id": "job-1"},
	}); insertErr != nil {
		//nolint:errcheck // Best-effort rollback before failing the test.
		_ = tx.Rollback(ctx)
		t.Fatalf("InsertTx: %v", insertErr)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatalf("Commit: %v", commitErr)
	}

	// Publish the event to Kafka.
	published, err := repo.PublishTopic(ctx, kafka.TopicJobs, zap.NewNop())
	if err != nil {
		t.Fatalf("PublishTopic: %v", err)
	}
	if published != 1 {
		t.Fatalf("published %d events, want 1", published)
	}

	// The event is marked PUBLISHED in the outbox.
	var status string
	if statusErr := pg.QueryRow(ctx, `SELECT status FROM outbox_events WHERE event_key = $1`, "event-"+t.Name()).Scan(&status); statusErr != nil {
		t.Fatalf("fetch outbox status: %v", statusErr)
	}
	if status != "PUBLISHED" {
		t.Fatalf("outbox status = %q, want %q", status, "PUBLISHED")
	}

	// The record lands on the jobs topic with the right key and payload. The
	// predicate must select this test's record specifically: other tests in
	// this package may have published unrelated jobs-topic records earlier,
	// and the consumer reads from the earliest offset.
	consumer := testkit.KafkaConsumer(t, "outboxrelay-"+t.Name(), kafka.TopicJobs)
	record := testkit.WaitForRecord(t, consumer, 15*time.Second, func(rec *kgo.Record) bool {
		return rec.Topic == kafka.TopicJobs && string(rec.Key) == "workflow-1"
	})
	if got := string(record.Key); got != "workflow-1" {
		t.Fatalf("record key = %q, want %q", got, "workflow-1")
	}
	if got := string(record.Value); got == "" {
		t.Fatal("record value is empty, want the payload")
	}

	// Nothing is left to publish: a second run is a no-op.
	again, err := repo.PublishTopic(ctx, kafka.TopicJobs, zap.NewNop())
	if err != nil {
		t.Fatalf("PublishTopic (second run): %v", err)
	}
	if again != 0 {
		t.Fatalf("second run published %d events, want 0", again)
	}
}

func TestIntegrationPublishTopicPreservesOrderingAcrossKeys(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(&Config{
		BatchSize:       100,
		MaxAttempts:     3,
		RetryBackoff:    time.Second,
		ProcessingLease: 30 * time.Second,
		WorkerID:        "integration-worker",
	}, pg, testkit.Kafka(t))

	// Two events for the same kafka_key; only the oldest unpublished one may be
	// claimed in a single run (per-key ordering).
	tx, err := pg.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	prefix := "order-" + t.Name()
	if insertErr := outbox.InsertTx(ctx, tx, &outbox.Event{Topic: kafka.TopicJobs, KafkaKey: "wf-x", EventKey: prefix + "-1", Payload: "first"}); insertErr != nil {
		//nolint:errcheck // Best-effort rollback before failing the test.
		_ = tx.Rollback(ctx)
		t.Fatalf("InsertTx 1: %v", insertErr)
	}
	if insertErr := outbox.InsertTx(ctx, tx, &outbox.Event{Topic: kafka.TopicJobs, KafkaKey: "wf-x", EventKey: prefix + "-2", Payload: "second"}); insertErr != nil {
		//nolint:errcheck // Best-effort rollback before failing the test.
		_ = tx.Rollback(ctx)
		t.Fatalf("InsertTx 2: %v", insertErr)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatalf("Commit: %v", commitErr)
	}

	published, err := repo.PublishTopic(ctx, kafka.TopicJobs, zap.NewNop())
	if err != nil {
		t.Fatalf("PublishTopic: %v", err)
	}
	if published != 1 {
		t.Fatalf("published %d events, want 1 (per-key ordering)", published)
	}

	var firstStatus, secondStatus string
	if statusErr := pg.QueryRow(ctx, `SELECT status FROM outbox_events WHERE event_key = $1`, prefix+"-1").Scan(&firstStatus); statusErr != nil {
		t.Fatalf("fetch first status: %v", statusErr)
	}
	if statusErr := pg.QueryRow(ctx, `SELECT status FROM outbox_events WHERE event_key = $1`, prefix+"-2").Scan(&secondStatus); statusErr != nil {
		t.Fatalf("fetch second status: %v", statusErr)
	}
	if firstStatus != "PUBLISHED" || secondStatus != "PENDING" {
		t.Fatalf("statuses = %q/%q, want PUBLISHED/PENDING", firstStatus, secondStatus)
	}

	// The second run publishes the remaining event.
	published, err = repo.PublishTopic(ctx, kafka.TopicJobs, zap.NewNop())
	if err != nil {
		t.Fatalf("PublishTopic (second run): %v", err)
	}
	if published != 1 {
		t.Fatalf("second run published %d events, want 1", published)
	}
}

func TestIntegrationCleanupCommandIdempotencyKeys(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)

	// Seed ledger rows: two expired completed keys, one live completed key and
	// one in-progress key (no expiry), all in a dedicated scope.
	scope := "cleanup-" + t.Name()
	hashCounter := 0
	seedKey := func(operation, key, status, expiresAtSQL string) {
		t.Helper()
		hashCounter++
		query := `
			INSERT INTO command_idempotency_keys
				(scope, operation, idempotency_key, request_hash, status, resource_id, response, created_at, updated_at, completed_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, 'resource-1', '{}', now() AT TIME ZONE 'utc' - interval '2 hours', now() AT TIME ZONE 'utc', now() AT TIME ZONE 'utc', ` + expiresAtSQL + `)
		`
		if _, err := pg.Exec(ctx, query, scope, operation, key, fmt.Sprintf("%064x", hashCounter), status); err != nil {
			t.Fatalf("seed %s/%s: %v", operation, key, err)
		}
	}
	seedKey("op", "expired-1", "COMPLETED", "now() AT TIME ZONE 'utc' - interval '1 hour'")
	seedKey("op", "expired-2", "COMPLETED", "now() AT TIME ZONE 'utc' - interval '30 minutes'")
	seedKey("op", "live", "COMPLETED", "now() AT TIME ZONE 'utc' + interval '1 hour'")
	if _, err := pg.Exec(ctx, `
		INSERT INTO command_idempotency_keys
			(scope, operation, idempotency_key, request_hash, status, created_at, updated_at)
		VALUES ($1, 'op', 'processing', $2, 'PROCESSING', now() AT TIME ZONE 'utc', now() AT TIME ZONE 'utc')
	`, scope, fmt.Sprintf("%064x", hashCounter+1)); err != nil {
		t.Fatalf("seed processing key: %v", err)
	}

	repo := New(&Config{
		BatchSize:       100,
		MaxAttempts:     3,
		RetryBackoff:    time.Second,
		ProcessingLease: 30 * time.Second,
		WorkerID:        "integration-worker",
	}, pg, testkit.Kafka(t))

	// A bounded batch deletes at most batchSize expired rows.
	deleted, err := repo.CleanupCommandIdempotencyKeys(ctx, 1)
	if err != nil {
		t.Fatalf("CleanupCommandIdempotencyKeys(1): %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted %d rows with batch size 1, want 1", deleted)
	}

	// The remaining run removes every other expired row and nothing else.
	deleted, err = repo.CleanupCommandIdempotencyKeys(ctx, 100)
	if err != nil {
		t.Fatalf("CleanupCommandIdempotencyKeys(100): %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted %d remaining expired rows, want 1", deleted)
	}

	var total int
	if countErr := pg.QueryRow(ctx, `SELECT count(*) FROM command_idempotency_keys WHERE scope = $1`, scope).Scan(&total); countErr != nil {
		t.Fatalf("count remaining keys: %v", countErr)
	}
	if total != 2 {
		t.Fatalf("remaining keys = %d, want 2 (live + processing)", total)
	}

	// Nothing expired is left: another run is a no-op.
	deleted, err = repo.CleanupCommandIdempotencyKeys(ctx, 100)
	if err != nil {
		t.Fatalf("CleanupCommandIdempotencyKeys (drained): %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted %d rows after drain, want 0", deleted)
	}
}
