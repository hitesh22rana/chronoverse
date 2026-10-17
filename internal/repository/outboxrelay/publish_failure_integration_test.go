//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package outboxrelay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// failingKafkaClient returns a producer whose broker dial always fails, so
// ProduceSync reports a real produce error without touching any broker.
func failingKafkaClient(t *testing.T) *kgo.Client {
	t.Helper()

	client, err := kgo.NewClient(
		kgo.SeedBrokers("127.0.0.1:1"),
		kgo.Dialer(func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("kafka broker unreachable")
		}),
		kgo.RecordRetries(0),
		kgo.RecordDeliveryTimeout(2*time.Second),
	)
	if err != nil {
		t.Fatalf("create failing kafka client: %v", err)
	}
	t.Cleanup(client.Close)

	return client
}

// insertPendingEvent stores a PENDING outbox event through the production
// insert helper and returns its generated id.
func insertPendingEvent(ctx context.Context, t *testing.T, pg *postgres.Postgres, topic, kafkaKey, eventKey string) string {
	t.Helper()

	tx, err := pg.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if insertErr := outbox.InsertTx(ctx, tx, &outbox.Event{
		Topic:    topic,
		KafkaKey: kafkaKey,
		EventKey: eventKey,
		Payload:  map[string]any{"job_id": "job-1"},
	}); insertErr != nil {
		//nolint:errcheck // Best-effort rollback before failing the test.
		_ = tx.Rollback(ctx)
		t.Fatalf("InsertTx: %v", insertErr)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var id string
	if err := pg.QueryRow(ctx, `SELECT id FROM outbox_events WHERE topic = $1 AND event_key = $2`, topic, eventKey).Scan(&id); err != nil {
		t.Fatalf("fetch inserted event id: %v", err)
	}

	return id
}

// outboxRowState is the persisted state the failure paths must produce.
type outboxRowState struct {
	Status          string
	Attempts        int
	LockedAtNull    bool
	LockedByNull    bool
	PublishedAtNull bool
	NextInSeconds   float64
}

func fetchOutboxState(ctx context.Context, t *testing.T, pg *postgres.Postgres, id string) outboxRowState {
	t.Helper()

	var state outboxRowState
	if err := pg.QueryRow(ctx, `
		SELECT status,
		       attempts,
		       locked_at IS NULL,
		       locked_by IS NULL,
		       published_at IS NULL,
		       EXTRACT(EPOCH FROM (next_attempt_at - (now() AT TIME ZONE 'utc')))::float8
		FROM outbox_events
		WHERE id = $1`, id).Scan(
		&state.Status,
		&state.Attempts,
		&state.LockedAtNull,
		&state.LockedByNull,
		&state.PublishedAtNull,
		&state.NextInSeconds,
	); err != nil {
		t.Fatalf("fetch outbox state: %v", err)
	}

	return state
}

func assertNextAttemptWithin(t *testing.T, seconds float64, want, tolerance time.Duration) {
	t.Helper()

	minSeconds := want.Seconds() - tolerance.Seconds()
	maxSeconds := want.Seconds() + tolerance.Seconds()
	if seconds < minSeconds || seconds > maxSeconds {
		t.Fatalf("next_attempt_at is %.1fs away, want %s +/- %s", seconds, want, tolerance)
	}
}

func TestIntegrationPublishFailureMarksFailedAndPreservesAttempts(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-publish-failure-" + t.Name()
	eventKey := topic + "-1"

	repo := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    10 * time.Minute,
		ProcessingLease: 30 * time.Second,
		WorkerID:        "failure-worker",
	}, pg, failingKafkaClient(t))

	id := insertPendingEvent(ctx, t, pg, topic, "wf-failure", eventKey)

	claimed, err := repo.PublishTopic(ctx, topic, zap.NewNop())
	if err != nil {
		t.Fatalf("PublishTopic: %v", err)
	}
	if claimed != 1 {
		t.Fatalf("PublishTopic claimed %d events, want 1", claimed)
	}

	state := fetchOutboxState(ctx, t, pg, id)
	if state.Status != statusFailed {
		t.Fatalf("status = %q, want FAILED", state.Status)
	}
	if state.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (claim increments; markFailed must not)", state.Attempts)
	}
	if !state.LockedAtNull || !state.LockedByNull {
		t.Fatalf("locks = atNull:%v byNull:%v, want released", state.LockedAtNull, state.LockedByNull)
	}
	if !state.PublishedAtNull {
		t.Fatal("published_at is set for a failed event, want NULL")
	}
	assertNextAttemptWithin(t, state.NextInSeconds, 10*time.Minute, 30*time.Second)

	// Open the retry window: a second failing publish claims the released row,
	// increments attempts once more and keeps it FAILED below the maximum.
	if _, execErr := pg.Exec(ctx, `UPDATE outbox_events SET next_attempt_at = (now() AT TIME ZONE 'utc') - interval '1 second' WHERE id = $1`, id); execErr != nil {
		t.Fatalf("open retry window: %v", execErr)
	}

	claimed, err = repo.PublishTopic(ctx, topic, zap.NewNop())
	if err != nil {
		t.Fatalf("PublishTopic (retry): %v", err)
	}
	if claimed != 1 {
		t.Fatalf("retry claimed %d events, want 1", claimed)
	}

	state = fetchOutboxState(ctx, t, pg, id)
	if state.Status != statusFailed {
		t.Fatalf("status after retry = %q, want FAILED", state.Status)
	}
	if state.Attempts != 2 {
		t.Fatalf("attempts after retry = %d, want 2", state.Attempts)
	}
	if !state.LockedAtNull || !state.LockedByNull {
		t.Fatalf("locks after retry = atNull:%v byNull:%v, want released", state.LockedAtNull, state.LockedByNull)
	}
}

func TestIntegrationPublishFailureMarksDeadAtMaxAttemptsWithDefaultBackoff(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-publish-dead-" + t.Name()

	repo := New(&Config{
		BatchSize:       10,
		MaxAttempts:     3,
		ProcessingLease: time.Minute,
		WorkerID:        "dead-worker",
	}, pg, failingKafkaClient(t))

	id := insertPendingEvent(ctx, t, pg, topic, "wf-dead", topic+"-1")
	if _, err := pg.Exec(ctx, `UPDATE outbox_events SET attempts = $2 WHERE id = $1`, id, 2); err != nil {
		t.Fatalf("seed attempt count: %v", err)
	}

	claimed, err := repo.PublishTopic(ctx, topic, zap.NewNop())
	if err != nil {
		t.Fatalf("PublishTopic: %v", err)
	}
	if claimed != 1 {
		t.Fatalf("PublishTopic claimed %d events, want 1", claimed)
	}

	state := fetchOutboxState(ctx, t, pg, id)
	if state.Status != statusDead {
		t.Fatalf("status = %q at maxAttempts, want DEAD", state.Status)
	}
	if state.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", state.Attempts)
	}
	if !state.LockedAtNull || !state.LockedByNull {
		t.Fatalf("locks = atNull:%v byNull:%v, want released", state.LockedAtNull, state.LockedByNull)
	}
	if !state.PublishedAtNull {
		t.Fatal("published_at is set for a dead event, want NULL")
	}
	assertNextAttemptWithin(t, state.NextInSeconds, 5*time.Second, 5*time.Second)

	claimed, err = repo.PublishTopic(ctx, topic, zap.NewNop())
	if err != nil {
		t.Fatalf("PublishTopic (after dead): %v", err)
	}
	if claimed != 0 {
		t.Fatalf("PublishTopic claimed %d dead events, want 0", claimed)
	}

	state = fetchOutboxState(ctx, t, pg, id)
	if state.Status != statusDead {
		t.Fatalf("status after exclusion run = %q, want DEAD", state.Status)
	}
}

func TestIntegrationPublishFailureWithStaleClaimReturnsAborted(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-publish-stale-" + t.Name()
	client := failingKafkaClient(t)

	repoA := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    time.Minute,
		ProcessingLease: 30 * time.Second,
		WorkerID:        "worker-a",
	}, pg, client)
	repoB := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    time.Minute,
		ProcessingLease: 30 * time.Second,
		WorkerID:        "worker-b",
	}, pg, client)

	id := insertPendingEvent(ctx, t, pg, topic, "wf-stale", topic+"-1")

	eventsA, err := repoA.claim(ctx, topic)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}
	if len(eventsA) != 1 {
		t.Fatalf("claim A returned %d events, want 1", len(eventsA))
	}

	// Expire A's lease so B may claim the same event.
	if _, execErr := pg.Exec(ctx, `UPDATE outbox_events SET locked_at = (now() AT TIME ZONE 'utc') - interval '5 minutes' WHERE id = $1`, id); execErr != nil {
		t.Fatalf("expire processing lease: %v", execErr)
	}

	eventsB, err := repoB.claim(ctx, topic)
	if err != nil {
		t.Fatalf("claim B: %v", err)
	}
	if len(eventsB) != 1 {
		t.Fatalf("claim B returned %d events, want 1", len(eventsB))
	}

	var lockedBy string
	if err := pg.QueryRow(ctx, `SELECT locked_by FROM outbox_events WHERE id = $1`, id).Scan(&lockedBy); err != nil {
		t.Fatalf("fetch claim owner: %v", err)
	}
	if !strings.HasPrefix(lockedBy, "worker-b:") {
		t.Fatalf("locked_by = %q, want worker-b claim", lockedBy)
	}

	// A lost its lease: both terminal updates must be rejected as Aborted and
	// must not touch the row B owns.
	if err := repoA.markPublished(ctx, eventsA[0]); status.Code(err) != codes.Aborted {
		t.Fatalf("stale markPublished error = %v, want Aborted", err)
	}
	if err := repoA.publishEvent(ctx, eventsA[0], zap.NewNop()); status.Code(err) != codes.Aborted {
		t.Fatalf("stale publishEvent error = %v, want Aborted", err)
	}

	stale := fetchOutboxState(ctx, t, pg, id)
	if stale.Status != statusProcessing || stale.Attempts != 2 {
		t.Fatalf("stale update changed row: status=%q attempts=%d, want PROCESSING/2", stale.Status, stale.Attempts)
	}
	if stale.LockedAtNull || stale.LockedByNull {
		t.Fatal("stale update released the owning claim")
	}
}

func TestIntegrationPublishFailureAfterReclaimPersistsOwnerFailure(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-publish-reclaim-" + t.Name()
	client := failingKafkaClient(t)

	repoA := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    time.Minute,
		ProcessingLease: 30 * time.Second,
		WorkerID:        "worker-a",
	}, pg, client)
	repoB := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    time.Minute,
		ProcessingLease: 30 * time.Second,
		WorkerID:        "worker-b",
	}, pg, client)

	id := insertPendingEvent(ctx, t, pg, topic, "wf-reclaim", topic+"-1")

	eventsA, err := repoA.claim(ctx, topic)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}
	if len(eventsA) != 1 {
		t.Fatalf("claim A returned %d events, want 1", len(eventsA))
	}

	// Expire A's lease so B may claim the same event.
	if _, execErr := pg.Exec(ctx, `UPDATE outbox_events SET locked_at = (now() AT TIME ZONE 'utc') - interval '5 minutes' WHERE id = $1`, id); execErr != nil {
		t.Fatalf("expire processing lease: %v", execErr)
	}

	eventsB, err := repoB.claim(ctx, topic)
	if err != nil {
		t.Fatalf("claim B: %v", err)
	}
	if len(eventsB) != 1 {
		t.Fatalf("claim B returned %d events, want 1", len(eventsB))
	}

	if err := repoB.publishEvent(ctx, eventsB[0], zap.NewNop()); err != nil {
		t.Fatalf("owner publishEvent: %v", err)
	}

	owner := fetchOutboxState(ctx, t, pg, id)
	if owner.Status != statusFailed || owner.Attempts != 2 {
		t.Fatalf("owner failure persisted status=%q attempts=%d, want FAILED/2", owner.Status, owner.Attempts)
	}
	if !owner.LockedAtNull || !owner.LockedByNull {
		t.Fatalf("owner locks = atNull:%v byNull:%v, want released", owner.LockedAtNull, owner.LockedByNull)
	}
}

func TestIntegrationPublishFailureDatabaseErrorsSurfaceInternal(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-publish-cancel-" + t.Name()

	repo := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    time.Minute,
		ProcessingLease: time.Minute,
		WorkerID:        "cancel-worker",
	}, pg, failingKafkaClient(t))

	id := insertPendingEvent(ctx, t, pg, topic, "wf-cancel", topic+"-1")

	events, err := repo.claim(ctx, topic)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("claim returned %d events, want 1", len(events))
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	if err := repo.markFailed(canceled, events[0]); status.Code(err) != codes.Internal {
		t.Fatalf("markFailed with canceled context = %v, want Internal", err)
	}
	if err := repo.markPublished(canceled, events[0]); status.Code(err) != codes.Internal {
		t.Fatalf("markPublished with canceled context = %v, want Internal", err)
	}
	if _, err := repo.PublishTopic(canceled, topic, zap.NewNop()); status.Code(err) != codes.Internal {
		t.Fatalf("PublishTopic with canceled context = %v, want Internal", err)
	}

	state := fetchOutboxState(ctx, t, pg, id)
	if state.Status != statusProcessing || state.Attempts != 1 {
		t.Fatalf("canceled writes changed row: status=%q attempts=%d, want PROCESSING/1", state.Status, state.Attempts)
	}
	if state.LockedAtNull || state.LockedByNull {
		t.Fatal("canceled writes released the claim")
	}
}

// sqlTestLiteral quotes a test-controlled event key for use in trigger DDL.
func sqlTestLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// installTerminalUpdateFailureTrigger makes the terminal marking UPDATE of one
// event key fail inside PostgreSQL, injecting a real database error without
// touching the claim UPDATE.
func installTerminalUpdateFailureTrigger(ctx context.Context, t *testing.T, pg *postgres.Postgres, eventKey string) {
	t.Helper()

	const functionName = "test_outbox_terminal_update_failure"
	const triggerName = "test_outbox_terminal_update_failure_trigger"

	ddl := fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'test-induced outbox terminal update failure';
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER %s
		BEFORE UPDATE ON outbox_events
		FOR EACH ROW
		WHEN (NEW.event_key = %s AND NEW.status IN ('FAILED', 'DEAD'))
		EXECUTE FUNCTION %s();`, functionName, triggerName, sqlTestLiteral(eventKey), functionName)
	if _, err := pg.Exec(ctx, ddl); err != nil {
		t.Fatalf("install terminal update failure trigger: %v", err)
	}

	t.Cleanup(func() {
		if _, err := pg.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON outbox_events; DROP FUNCTION IF EXISTS %s();`, triggerName, functionName)); err != nil {
			t.Logf("drop terminal update failure trigger: %v", err)
		}
	})
}

// installDeferredClaimCommitFailureTrigger makes the claim transaction fail at
// COMMIT for one event key through a deferred constraint trigger.
func installDeferredClaimCommitFailureTrigger(ctx context.Context, t *testing.T, pg *postgres.Postgres, eventKey string) {
	t.Helper()

	const functionName = "test_outbox_claim_commit_failure"
	const triggerName = "test_outbox_claim_commit_failure_trigger"

	ddl := fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'test-induced outbox claim commit failure';
		END;
		$$ LANGUAGE plpgsql;
		CREATE CONSTRAINT TRIGGER %s
		AFTER UPDATE ON outbox_events
		DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW
		WHEN (NEW.event_key = %s)
		EXECUTE FUNCTION %s();`, functionName, triggerName, sqlTestLiteral(eventKey), functionName)
	if _, err := pg.Exec(ctx, ddl); err != nil {
		t.Fatalf("install deferred claim commit failure trigger: %v", err)
	}

	t.Cleanup(func() {
		if _, err := pg.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON outbox_events; DROP FUNCTION IF EXISTS %s();`, triggerName, functionName)); err != nil {
			t.Logf("drop deferred claim commit failure trigger: %v", err)
		}
	})
}

func TestIntegrationPublishFailureMarkFailureDatabaseErrorSurfacesInternal(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-publish-mark-error-" + t.Name()
	eventKey := topic + "-1"

	repo := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    time.Minute,
		ProcessingLease: time.Minute,
		WorkerID:        "mark-error-worker",
	}, pg, failingKafkaClient(t))

	installTerminalUpdateFailureTrigger(ctx, t, pg, eventKey)

	id := insertPendingEvent(ctx, t, pg, topic, "wf-mark-error", eventKey)

	claimed, err := repo.PublishTopic(ctx, topic, zap.NewNop())
	if status.Code(err) != codes.Internal {
		t.Fatalf("PublishTopic with failing marking update = %v, want Internal", err)
	}
	if claimed != 1 {
		t.Fatalf("PublishTopic claimed %d events, want 1", claimed)
	}

	state := fetchOutboxState(ctx, t, pg, id)
	if state.Status != statusProcessing || state.Attempts != 1 {
		t.Fatalf("state after failed marking = %q/%d, want PROCESSING/1", state.Status, state.Attempts)
	}
	if state.LockedAtNull || state.LockedByNull {
		t.Fatal("failed marking released the claim")
	}
}

func TestIntegrationClaimCommitFailureSurfacesInternal(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-claim-commit-error-" + t.Name()
	eventKey := topic + "-1"

	repo := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    time.Minute,
		ProcessingLease: time.Minute,
		WorkerID:        "claim-error-worker",
	}, pg, failingKafkaClient(t))

	installDeferredClaimCommitFailureTrigger(ctx, t, pg, eventKey)

	id := insertPendingEvent(ctx, t, pg, topic, "wf-claim-error", eventKey)

	if _, err := repo.PublishTopic(ctx, topic, zap.NewNop()); status.Code(err) != codes.Internal {
		t.Fatalf("PublishTopic with failing claim commit = %v, want Internal", err)
	}

	state := fetchOutboxState(ctx, t, pg, id)
	if state.Status != statusPending || state.Attempts != 0 {
		t.Fatalf("state after failed claim commit = %q/%d, want PENDING/0", state.Status, state.Attempts)
	}
	if !state.LockedAtNull || !state.LockedByNull {
		t.Fatal("failed claim commit left locks behind")
	}
	if !state.PublishedAtNull {
		t.Fatal("failed claim commit published the event")
	}
}
