//nolint:testpackage // Uses shared PostgreSQL and Kafka relay fixtures.
package outboxrelay

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationRelayRepublishesUnfinishedCleanupWithoutResettingBudget(t *testing.T) {
	pg := testkit.Postgres(t)
	topic := "workflows"
	past := time.Now().Add(-10 * time.Minute)
	for _, state := range []string{"READY", "RETRY", "PROCESSING", "QUARANTINED", "COMPLETED"} {
		seedOutboxRow(t.Context(), t, pg, topic, state, statusPublished, &past)
		if _, err := pg.Exec(t.Context(), `UPDATE outbox_events SET kafka_key=event_key,cleanup_replay_due_at=published_at,
 cleanup_state=$2,cleanup_attempts=2,cleanup_generation=4 WHERE topic=$1 AND event_key=$2`, topic, state); err != nil {
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
 cleanup_replay_due_at=CASE WHEN event_key='recent' THEN now() AT TIME ZONE 'utc'+interval '1 hour' ELSE published_at END,
 published_at=CASE WHEN event_key='recent' THEN now() AT TIME ZONE 'utc' ELSE published_at END WHERE topic=$1 AND event_key=$2`, topic, name); err != nil {
			t.Fatal(err)
		}
	}
}

func resetReplayProbe(t *testing.T) {
	t.Helper()
	pg := testkit.Postgres(t)
	_, err := pg.Exec(t.Context(), `TRUNCATE outbox_events CASCADE; UPDATE outbox_relay_turns SET next_replay=FALSE`)
	require.NoError(t, err)
}

func seedReplayLane(t *testing.T, prefix string, count int, replay bool) {
	t.Helper()
	pg := testkit.Postgres(t)
	for i := 0; i < count; i++ {
		_, err := pg.Exec(t.Context(), `INSERT INTO outbox_events(topic,kafka_key,event_key,payload,created_at,cleanup_state,cleanup_replay_round)
 VALUES('workflows',$1,$1,'{}',CASE WHEN $2 THEN now()-interval '1 day' ELSE now() END,CASE WHEN $2 THEN 'READY' END,CASE WHEN $2 THEN 1 ELSE 0 END)`, fmt.Sprintf("%s-%04d", prefix, i), replay)
		require.NoError(t, err)
	}
}

func TestIntegrationReplayFairAllocationAndSendOrder(t *testing.T) {
	for _, batch := range []int{1, 2, 3, 100} {
		t.Run(fmt.Sprint(batch), func(t *testing.T) {
			resetReplayProbe(t)
			seedReplayLane(t, "replay", batch*2, true)
			seedReplayLane(t, "initial", batch*2, false)
			pg := testkit.Postgres(t)
			// A fresh repository per poll proves turn state is not process-local or time parity.
			for poll := 0; poll < 2; poll++ {
				repo := New(&Config{BatchSize: batch, WorkerID: "same-worker"}, pg, nil)
				events, err := repo.claim(t.Context(), "workflows")
				require.NoError(t, err)
				require.Len(t, events, batch)
				for i, event := range events {
					require.Equal(t, (poll+i)%2 == 1, event.ReplayRound > 0, "poll=%d send position=%d", poll, i)
					require.NoError(t, repo.markPublished(t.Context(), event))
				}
			}
		})
	}
}

func TestIntegrationReplayBorrowsUnusedLaneCapacity(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(fmt.Sprint(replay), func(t *testing.T) {
			resetReplayProbe(t)
			seedReplayLane(t, "only", 5, replay)
			repo := New(&Config{BatchSize: 5}, testkit.Postgres(t), nil)
			events, err := repo.claim(t.Context(), "workflows")
			require.NoError(t, err)
			require.Len(t, events, 5)
			for i, event := range events {
				require.Equal(t, fmt.Sprintf("only-%04d", i), event.EventKey)
			}
		})
	}
}

func TestIntegrationReplayActualPublicationProgress(t *testing.T) {
	resetReplayProbe(t)
	seedReplayLane(t, "old", 12, true)
	seedReplayLane(t, "new", 12, false)
	pg := testkit.Postgres(t)
	consumer := testkit.KafkaRecordConsumer(t, "workflows")
	for poll := 0; poll < 4; poll++ {
		repo := New(&Config{BatchSize: 1}, pg, testkit.Kafka(t))
		count, err := repo.PublishTopic(t.Context(), "workflows", zap.NewNop())
		require.NoError(t, err)
		require.Equal(t, 1, count)
	}
	wanted := map[string]bool{"new-0000": false, "old-0000": false, "new-0001": false, "old-0001": false}
	remaining := len(wanted)
	testkit.WaitForRecord(t, consumer, 10*time.Second, func(record *kgo.Record) bool {
		if seen, ok := wanted[string(record.Key)]; ok && !seen {
			wanted[string(record.Key)] = true
			remaining--
		}
		return remaining == 0
	})
	var initial, replay int
	require.NoError(t, pg.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE cleanup_replay_round=0),
 count(*) FILTER(WHERE cleanup_replay_round>0) FROM outbox_events WHERE status='PUBLISHED'`).Scan(&initial, &replay))
	require.Equal(t, 2, initial)
	require.Equal(t, 2, replay)
}

func TestIntegrationReplayPacingSurvivesRepositoryRestart(t *testing.T) {
	resetReplayProbe(t)
	pg := testkit.Postgres(t)
	seedReplayLane(t, "paced", 1, true)
	_, err := pg.Exec(t.Context(), `UPDATE outbox_events SET cleanup_replay_round=0,cleanup_attempts=2,cleanup_generation=4`)
	require.NoError(t, err)
	for round := 0; round <= 13; round++ {
		repo := New(&Config{BatchSize: 1, ReplayInterval: time.Millisecond, ReplayMaxInterval: time.Hour}, pg, nil)
		if round > 0 {
			// Publication is due only after its persisted deadline, not merely old published_at.
			_, err = pg.Exec(t.Context(), `UPDATE outbox_events SET cleanup_replay_due_at=now()-interval '1 second'`)
			require.NoError(t, err)
			require.NoError(t, repo.requeueUnfinishedCleanup(t.Context(), "workflows"))
		}
		events, claimErr := repo.claim(t.Context(), "workflows")
		require.NoError(t, claimErr)
		require.Len(t, events, 1)
		require.Equal(t, round, events[0].ReplayRound)
		require.NoError(t, repo.markPublished(t.Context(), events[0]))
		var delay float64
		var attempts int
		var generation int64
		require.NoError(t, pg.QueryRow(t.Context(), `SELECT EXTRACT(EPOCH FROM(cleanup_replay_due_at-published_at)),
 cleanup_attempts,cleanup_generation FROM outbox_events`).Scan(&delay, &attempts, &generation))
		want := time.Millisecond * time.Duration(1<<round)
		require.InDelta(t, want.Seconds(), delay, 0.000001)
		require.Equal(t, 2, attempts)
		require.Equal(t, int64(4), generation)
		// Use a generous future deadline to avoid a 1ms assertion depending on host speed.
		_, err = pg.Exec(t.Context(), `UPDATE outbox_events SET published_at=now()-interval '1 day',cleanup_replay_due_at=now()+interval '1 hour'`)
		require.NoError(t, err)
		require.NoError(t, repo.requeueUnfinishedCleanup(t.Context(), "workflows"))
		var state string
		require.NoError(t, pg.QueryRow(t.Context(), `SELECT status FROM outbox_events`).Scan(&state))
		require.Equal(t, "PUBLISHED", state)
	}
	// Saturation is not a lifetime replay limit and is not tied to five rounds.
	_, err = pg.Exec(t.Context(), `UPDATE outbox_events SET cleanup_replay_round=63,cleanup_replay_due_at=now()-interval '1 second'`)
	require.NoError(t, err)
	repo := New(&Config{BatchSize: 1, ReplayInterval: time.Millisecond, ReplayMaxInterval: time.Hour}, pg, nil)
	require.NoError(t, repo.requeueUnfinishedCleanup(t.Context(), "workflows"))
	events, err := repo.claim(t.Context(), "workflows")
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, 63, events[0].ReplayRound)
	require.NoError(t, repo.markPublished(t.Context(), events[0]))
	var seconds float64
	require.NoError(t, pg.QueryRow(t.Context(), `SELECT EXTRACT(EPOCH FROM(cleanup_replay_due_at-published_at)) FROM outbox_events`).Scan(&seconds))
	require.Equal(t, 3600.0, seconds)
}

func TestIntegrationReplayConcurrentReplicaTurns(t *testing.T) {
	resetReplayProbe(t)
	seedReplayLane(t, "replay", 4, true)
	seedReplayLane(t, "initial", 4, false)
	pg := testkit.Postgres(t)
	results := make(chan []*Event, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			repo := New(&Config{BatchSize: 1, WorkerID: "same"}, pg, nil)
			events, err := repo.claim(t.Context(), "workflows")
			results <- events
			errs <- err
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	lanes := map[bool]int{}
	ids := map[string]bool{}
	for events := range results {
		require.Len(t, events, 1)
		require.False(t, ids[events[0].ID])
		ids[events[0].ID] = true
		lanes[events[0].ReplayRound > 0]++
	}
	require.Equal(t, 1, lanes[false])
	require.Equal(t, 1, lanes[true])
}

func TestIntegrationReplayPreservesCrossLaneKeyBarriers(t *testing.T) {
	for _, state := range []string{"PENDING", "PROCESSING", "FAILED", "DEAD", "PUBLISHED"} {
		for _, earlierReplay := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", state, earlierReplay), func(t *testing.T) {
				resetReplayProbe(t)
				pg := testkit.Postgres(t)
				seedReplayLane(t, "earlier", 1, earlierReplay)
				seedReplayLane(t, "later", 1, !earlierReplay)
				_, err := pg.Exec(t.Context(), `UPDATE outbox_events SET kafka_key='same',created_at=now()-interval '2 days',
 status=$1::OUTBOX_STATUS,next_attempt_at=now()+interval '1 hour',locked_at=now() WHERE event_key='earlier-0000'`, state)
				require.NoError(t, err)
				_, err = pg.Exec(t.Context(), `UPDATE outbox_events SET kafka_key='same' WHERE event_key='later-0000'`)
				require.NoError(t, err)
				events, err := New(&Config{BatchSize: 3}, pg, nil).claim(t.Context(), "workflows")
				require.NoError(t, err)
				if state == "PUBLISHED" {
					require.Len(t, events, 1)
					require.Equal(t, "later-0000", events[0].EventKey)
				} else {
					require.Empty(t, events)
				}
			})
		}
	}
}

func TestIntegrationReplayExpiredClaimStaysInWaveAndFencesPacing(t *testing.T) {
	resetReplayProbe(t)
	pg := testkit.Postgres(t)
	seedReplayLane(t, "wave", 1, true)
	repo := New(&Config{BatchSize: 1, MaxAttempts: 2, WorkerID: "same"}, pg, nil)
	first, err := repo.claim(t.Context(), "workflows")
	require.NoError(t, err)
	require.Len(t, first, 1)
	_, err = pg.Exec(t.Context(), `UPDATE outbox_events SET locked_at=now()-interval '1 minute'`)
	require.NoError(t, err)
	replacement, err := New(&Config{BatchSize: 1, MaxAttempts: 2, WorkerID: "same"}, pg, nil).claim(t.Context(), "workflows")
	require.NoError(t, err)
	require.Len(t, replacement, 1)
	require.Equal(t, 1, replacement[0].ReplayRound)
	require.Equal(t, codes.Aborted, status.Code(repo.markPublished(t.Context(), first[0])))
	require.Equal(t, codes.Aborted, status.Code(repo.markFailed(t.Context(), first[0])))
	require.NoError(t, repo.markFailed(t.Context(), replacement[0]))
	require.NoError(t, repo.requeueUnfinishedCleanup(t.Context(), "workflows"))
	var state string
	var round int
	var due bool
	require.NoError(t, pg.QueryRow(t.Context(), `SELECT status,cleanup_replay_round,cleanup_replay_due_at IS NOT NULL FROM outbox_events`).Scan(&state, &round, &due))
	require.Equal(t, "DEAD", state)
	require.Equal(t, 1, round)
	require.False(t, due)
}

func TestIntegrationReplayAllocationFaultsRollbackTurnAndClaims(t *testing.T) {
	for _, fault := range []string{"missing turn", "first lane", "second lane", "borrow", "advance turn"} {
		t.Run(fault, func(t *testing.T) {
			resetReplayProbe(t)
			seedReplayLane(t, "initial", 4, false)
			seedReplayLane(t, "replay", 4, true)
			pg := testkit.Postgres(t)
			if fault == "missing turn" {
				_, err := pg.Exec(t.Context(), `DELETE FROM outbox_relay_turns`)
				require.NoError(t, err)
				t.Cleanup(func() {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_, err := pg.Exec(cleanupCtx, `INSERT INTO outbox_relay_turns(topic,next_replay) VALUES('workflows',FALSE)`)
					require.NoError(t, err)
				})
			} else {
				table, target := "outbox_events", "initial-0000"
				switch fault {
				case "second lane":
					target = "replay-0000"
				case "borrow":
					target = "initial-0003"
					_, err := pg.Exec(t.Context(), `DELETE FROM outbox_events WHERE cleanup_replay_round>0`)
					require.NoError(t, err)
				case "advance turn":
					table = "outbox_relay_turns"
				}
				condition := fmt.Sprintf("NEW.event_key='%s'", target)
				if fault == "advance turn" {
					condition = "TRUE"
				}
				_, err := pg.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION cv017_allocation_fault() RETURNS TRIGGER LANGUAGE plpgsql AS $$
 BEGIN IF %s THEN RAISE EXCEPTION 'allocation fault'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER cv017_allocation_fault BEFORE UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION cv017_allocation_fault()`, condition, table))
				require.NoError(t, err)
				t.Cleanup(func() {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_, err := pg.Exec(cleanupCtx, fmt.Sprintf(`DROP TRIGGER cv017_allocation_fault ON %s; DROP FUNCTION cv017_allocation_fault()`, table))
					require.NoError(t, err)
				})
			}
			events, err := New(&Config{BatchSize: 5}, pg, nil).claim(t.Context(), "workflows")
			require.Error(t, err)
			require.Empty(t, events)
			var claimed int
			require.NoError(t, pg.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events WHERE status='PROCESSING'`).Scan(&claimed))
			require.Zero(t, claimed)
			if fault != "missing turn" {
				var next bool
				require.NoError(t, pg.QueryRow(t.Context(), `SELECT next_replay FROM outbox_relay_turns`).Scan(&next))
				require.False(t, next)
			}
		})
	}
}

func TestIntegrationReplayPartialPublicationRotatesFirstSend(t *testing.T) {
	resetReplayProbe(t)
	seedReplayLane(t, "partial-initial", 4, false)
	seedReplayLane(t, "partial-replay", 4, true)
	pg := testkit.Postgres(t)
	repo := New(&Config{BatchSize: 4}, pg, testkit.Kafka(t))
	events, err := repo.claim(t.Context(), "workflows")
	require.NoError(t, err)
	require.Len(t, events, 4)
	require.Zero(t, events[0].ReplayRound)
	interrupted, cancel := context.WithCancel(t.Context())
	require.NoError(t, repo.publishEvent(interrupted, events[0], zap.NewNop()))
	cancel()
	// A context expiring after the first actual send leaves unacknowledged claims recoverable.
	require.Error(t, repo.publishEvent(interrupted, events[1], zap.NewNop()))
	_, err = pg.Exec(t.Context(), `UPDATE outbox_events SET locked_at=now()-interval '1 minute' WHERE status='PROCESSING'`)
	require.NoError(t, err)
	replacement := New(&Config{BatchSize: 4}, pg, testkit.Kafka(t))
	events, err = replacement.claim(t.Context(), "workflows")
	require.NoError(t, err)
	require.Len(t, events, 4)
	require.Positive(t, events[0].ReplayRound)
	require.NoError(t, replacement.publishEvent(t.Context(), events[0], zap.NewNop()))
	consumer := testkit.KafkaRecordConsumer(t, "workflows")
	seen := map[string]bool{}
	testkit.WaitForRecord(t, consumer, 10*time.Second, func(record *kgo.Record) bool {
		key := string(record.Key)
		if key == "partial-initial-0000" || key == "partial-replay-0000" {
			seen[key] = true
		}
		return len(seen) == 2
	})
}
