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

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
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

// isolatedPostgres returns a fresh pool with an empty prepared statement
// cache. Temporary schema swaps cannot be exercised through the shared pool,
// whose connections may still hold plans bound to the previous relation.
func isolatedPostgres(t *testing.T) *postgres.Postgres {
	t.Helper()

	dsnConfig, err := pgx.ParseConfig(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatalf("parse postgres DSN: %v", err)
	}
	isolated, err := postgres.New(context.Background(), &postgres.Config{
		Host:        dsnConfig.Host,
		Port:        int(dsnConfig.Port),
		User:        dsnConfig.User,
		Password:    dsnConfig.Password,
		Database:    dsnConfig.Database,
		MaxConns:    2,
		DialTimeout: 5 * time.Second,
		TLSConfig:   &postgres.TLSConfig{Enabled: false},
	})
	if err != nil {
		t.Fatalf("create isolated postgres pool: %v", err)
	}
	t.Cleanup(isolated.Close)

	return isolated
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
	Status           string
	Attempts         int
	LockedAtNull     bool
	LockedByNull     bool
	PublishedAtNull  bool
	ScheduledSeconds float64
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
		       EXTRACT(EPOCH FROM (next_attempt_at - updated_at))::float8
		FROM outbox_events
		WHERE id = $1`, id).Scan(
		&state.Status,
		&state.Attempts,
		&state.LockedAtNull,
		&state.LockedByNull,
		&state.PublishedAtNull,
		&state.ScheduledSeconds,
	); err != nil {
		t.Fatalf("fetch outbox state: %v", err)
	}

	return state
}

// assertScheduledBackoff checks the persisted next_attempt_at - updated_at
// distance. Both columns come from the same SQL now() inside markFailed, so
// the configured backoff is asserted without depending on query latency.
func assertScheduledBackoff(t *testing.T, scheduledSeconds float64, want time.Duration) {
	t.Helper()

	const toleranceSeconds = 0.001
	if diff := scheduledSeconds - want.Seconds(); diff < -toleranceSeconds || diff > toleranceSeconds {
		t.Fatalf("scheduled backoff is %.6fs, want %s", scheduledSeconds, want)
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
	assertScheduledBackoff(t, state.ScheduledSeconds, 10*time.Minute)

	claimed, err = repo.PublishTopic(ctx, topic, zap.NewNop())
	require.NoError(t, err)
	require.Zero(t, claimed, "must not retry before backoff expires")
	require.Equal(t, state, fetchOutboxState(ctx, t, pg, id), "early retry changed row")

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
	assertScheduledBackoff(t, state.ScheduledSeconds, 5*time.Second)

	// Make the retry due so only the DEAD status can exclude this row.
	if _, execErr := pg.Exec(ctx, `UPDATE outbox_events SET next_attempt_at = (now() AT TIME ZONE 'utc') - interval '1 second' WHERE id = $1`, id); execErr != nil {
		t.Fatalf("open dead event retry window: %v", execErr)
	}

	claimed, err = repo.PublishTopic(ctx, topic, zap.NewNop())
	if err != nil {
		t.Fatalf("PublishTopic (after dead): %v", err)
	}
	if claimed != 0 {
		t.Fatalf("PublishTopic claimed %d dead events, want 0", claimed)
	}

	state = fetchOutboxState(ctx, t, pg, id)
	if state.Status != statusDead || state.Attempts != 3 {
		t.Fatalf("state after exclusion run = %q/%d, want DEAD/3", state.Status, state.Attempts)
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
	var ownerAfter string
	require.NoError(t, pg.QueryRow(ctx, `SELECT locked_by FROM outbox_events WHERE id = $1`, id).Scan(&ownerAfter))
	require.Equal(t, lockedBy, ownerAfter, "stale writes changed claim owner")
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
	if !owner.PublishedAtNull {
		t.Fatal("owner failure set published_at, want NULL")
	}
	assertScheduledBackoff(t, owner.ScheduledSeconds, time.Minute)
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

// installClaimUpdateFailureTrigger makes the claim UPDATE of one event key fail
// inside PostgreSQL after the statement starts. pgx stores execution errors in
// the returned rows, so the repository only observes this failure while
// iterating the claim result, not from the Query call itself.
func installClaimUpdateFailureTrigger(ctx context.Context, t *testing.T, pg *postgres.Postgres, eventKey string) {
	t.Helper()

	const functionName = "test_outbox_claim_update_failure"
	const triggerName = "test_outbox_claim_update_failure_trigger"

	ddl := fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'test-induced outbox claim update failure';
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER %s
		BEFORE UPDATE ON outbox_events
		FOR EACH ROW
		WHEN (NEW.event_key = %s AND NEW.status = 'PROCESSING')
		EXECUTE FUNCTION %s();`, functionName, triggerName, sqlTestLiteral(eventKey), functionName)
	if _, err := pg.Exec(ctx, ddl); err != nil {
		t.Fatalf("install claim update failure trigger: %v", err)
	}

	t.Cleanup(func() {
		if _, err := pg.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON outbox_events; DROP FUNCTION IF EXISTS %s();`, triggerName, functionName)); err != nil {
			t.Logf("drop claim update failure trigger: %v", err)
		}
	})
}

func TestIntegrationClaimUpdateFailureSurfacesInternal(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-claim-update-error-" + t.Name()
	eventKey := topic + "-1"

	repo := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    time.Minute,
		ProcessingLease: time.Minute,
		WorkerID:        "claim-update-error-worker",
	}, pg, failingKafkaClient(t))

	installClaimUpdateFailureTrigger(ctx, t, pg, eventKey)

	id := insertPendingEvent(ctx, t, pg, topic, "wf-claim-update-error", eventKey)

	claimed, err := repo.PublishTopic(ctx, topic, zap.NewNop())
	if status.Code(err) != codes.Internal {
		t.Fatalf("PublishTopic with failing claim update = %v, want Internal", err)
	}
	if message := status.Convert(err).Message(); !strings.HasPrefix(message, "failed to iterate outbox events") {
		t.Fatalf("error message = %q, want iteration failure", message)
	}
	if claimed != 0 {
		t.Fatalf("PublishTopic claimed %d events, want 0", claimed)
	}

	state := fetchOutboxState(ctx, t, pg, id)
	if state.Status != statusPending || state.Attempts != 0 {
		t.Fatalf("state after failed claim = %q/%d, want PENDING/0", state.Status, state.Attempts)
	}
	if !state.LockedAtNull || !state.LockedByNull {
		t.Fatal("failed claim left locks behind")
	}
	if !state.PublishedAtNull {
		t.Fatal("failed claim published the event")
	}
}

// TestIntegrationClaimQueryFailureSurfacesInternal simulates a schema outage:
// outbox_events is temporarily renamed and a fresh pool with an empty prepared
// statement cache makes claim.Query fail during preparation, before iteration.
func TestIntegrationClaimQueryFailureSurfacesInternal(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-claim-query-error-" + t.Name()

	id := insertPendingEvent(ctx, t, pg, topic, "wf-claim-query-error", topic+"-1")

	const hiddenTable = "outbox_events_claim_query_failure"
	if _, err := pg.Exec(ctx, `SET lock_timeout = '10s'; ALTER TABLE outbox_events RENAME TO `+hiddenTable+`;`); err != nil {
		t.Fatalf("hide outbox_events: %v", err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		if _, err := pg.Exec(context.Background(), `SET lock_timeout = '10s'; ALTER TABLE IF EXISTS `+hiddenTable+` RENAME TO outbox_events;`); err != nil {
			t.Fatalf("restore outbox_events: %v", err)
		}
		restored = true
	}
	defer restore()

	isolated := isolatedPostgres(t)

	repo := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    time.Minute,
		ProcessingLease: time.Minute,
		WorkerID:        "claim-query-error-worker",
	}, isolated, failingKafkaClient(t))

	claimed, err := repo.PublishTopic(ctx, topic, zap.NewNop())
	if status.Code(err) != codes.Internal {
		t.Fatalf("PublishTopic with missing outbox relation = %v, want Internal", err)
	}
	if message := status.Convert(err).Message(); !strings.HasPrefix(message, "failed to claim outbox events") {
		t.Fatalf("error message = %q, want prefix %q", message, "failed to claim outbox events")
	}
	if claimed != 0 {
		t.Fatalf("PublishTopic claimed %d events, want 0", claimed)
	}

	isolated.Close()
	restore()

	state := fetchOutboxState(ctx, t, pg, id)
	if state.Status != statusPending || state.Attempts != 0 {
		t.Fatalf("state after failed claim query = %q/%d, want PENDING/0", state.Status, state.Attempts)
	}
	if !state.LockedAtNull || !state.LockedByNull {
		t.Fatal("failed claim query left locks behind")
	}
	if !state.PublishedAtNull {
		t.Fatal("failed claim query published the event")
	}
}

// TestIntegrationClaimScanFailureSurfacesInternal injects schema drift: the
// real relation is temporarily replaced by a stand-in whose attempts column is
// numeric, so the claim UPDATE succeeds but scanning the fractional returned
// value fails. The production schema uses NOT NULL columns with fixed types,
// so this scan guard cannot fail without such drift.
func TestIntegrationClaimScanFailureSurfacesInternal(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	topic := "test-claim-scan-error-" + t.Name()

	const hiddenTable = "outbox_events_claim_scan_failure"
	if _, err := pg.Exec(ctx, `SET lock_timeout = '10s'; ALTER TABLE outbox_events RENAME TO `+hiddenTable+`;`); err != nil {
		t.Fatalf("hide outbox_events: %v", err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		if _, err := pg.Exec(context.Background(), `SET lock_timeout = '10s'; DROP TABLE IF EXISTS outbox_events; ALTER TABLE IF EXISTS `+hiddenTable+` RENAME TO outbox_events;`); err != nil {
			t.Fatalf("restore outbox_events: %v", err)
		}
		restored = true
	}
	defer restore()

	// The stand-in mirrors the claim query's column surface but returns the
	// fractional numeric, which *int cannot scan.
	if _, err := pg.Exec(ctx, `
		CREATE TABLE outbox_events (
			id UUID PRIMARY KEY,
			topic TEXT NOT NULL,
			kafka_key TEXT NOT NULL,
			event_key TEXT NOT NULL,
			payload JSONB NOT NULL,
			status TEXT NOT NULL,
			attempts NUMERIC NOT NULL DEFAULT 0,
			next_attempt_at TIMESTAMP NOT NULL DEFAULT (now() AT TIME ZONE 'utc'),
			locked_at TIMESTAMP,
			locked_by TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT (now() AT TIME ZONE 'utc')
		);`); err != nil {
		t.Fatalf("create scan-mismatch stand-in: %v", err)
	}
	if _, err := pg.Exec(ctx, `
		INSERT INTO outbox_events (id, topic, kafka_key, event_key, payload, status, attempts, next_attempt_at)
		VALUES ('00000000-0000-0000-0000-000000000001', $1, 'wf-claim-scan-error', $2, '{"job_id":"job-1"}', 'PENDING', 1.5, (now() AT TIME ZONE 'utc') - interval '1 minute');`,
		topic, topic+"-1"); err != nil {
		t.Fatalf("seed scan-mismatch row: %v", err)
	}

	isolated := isolatedPostgres(t)

	repo := New(&Config{
		BatchSize:       10,
		MaxAttempts:     5,
		RetryBackoff:    time.Minute,
		ProcessingLease: time.Minute,
		WorkerID:        "claim-scan-error-worker",
	}, isolated, failingKafkaClient(t))

	claimed, err := repo.PublishTopic(ctx, topic, zap.NewNop())
	if status.Code(err) != codes.Internal {
		t.Fatalf("PublishTopic with scan-mismatched claim result = %v, want Internal", err)
	}
	if message := status.Convert(err).Message(); !strings.HasPrefix(message, "failed to scan outbox event") {
		t.Fatalf("error message = %q, want prefix %q", message, "failed to scan outbox event")
	}
	if claimed != 0 {
		t.Fatalf("PublishTopic claimed %d events, want 0", claimed)
	}

	// The failed claim transaction rolled back: the stand-in row is untouched.
	var (
		statusValue string
		attempts    float64
	)
	if err := pg.QueryRow(ctx, `SELECT status, attempts FROM outbox_events WHERE topic = $1`, topic).Scan(&statusValue, &attempts); err != nil {
		t.Fatalf("fetch stand-in state: %v", err)
	}
	if statusValue != statusPending || attempts != 1.5 {
		t.Fatalf("state after failed claim scan = %q/%v, want PENDING/1.5", statusValue, attempts)
	}

	isolated.Close()
	restore()
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
	if message := status.Convert(err).Message(); !strings.HasPrefix(message, "failed to mark outbox event failed") {
		t.Fatalf("error message = %q, want terminal marking failure", message)
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

	claimed, err := repo.PublishTopic(ctx, topic, zap.NewNop())
	if status.Code(err) != codes.Internal {
		t.Fatalf("PublishTopic with failing claim commit = %v, want Internal", err)
	}
	if message := status.Convert(err).Message(); !strings.HasPrefix(message, "failed to commit outbox claim") {
		t.Fatalf("error message = %q, want claim commit failure", message)
	}
	if claimed != 0 {
		t.Fatalf("PublishTopic claimed %d events, want 0", claimed)
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
