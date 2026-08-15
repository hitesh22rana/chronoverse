//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package outboxrelay

import (
	"context"
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

	// The record lands on the jobs topic with the right key and payload.
	consumer := testkit.KafkaConsumer(t, "outboxrelay-"+t.Name(), kafka.TopicJobs)
	record := testkit.WaitForRecord(t, consumer, 15*time.Second, func(rec *kgo.Record) bool {
		return rec.Topic == kafka.TopicJobs
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
