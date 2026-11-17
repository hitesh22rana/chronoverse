//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package outboxrelay

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
	jobsrepo "github.com/hitesh22rana/chronoverse/internal/repository/jobs"
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
	pg := testkit.Postgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	topic := "test-cleanup-published-" + t.Name()
	repo := New(&Config{BatchSize: 10, MaxAttempts: 3, RetryBackoff: time.Minute, ProcessingLease: time.Minute, WorkerID: "cleanup-worker"}, pg, nil)

	now := time.Now().UTC()
	old3h := now.Add(-3 * time.Hour)
	old2h := now.Add(-2 * time.Hour)
	old90m := now.Add(-90 * time.Minute)
	recent := now.Add(-time.Minute)
	veryOld := now.Add(-5 * time.Hour)

	seedOutboxRow(ctx, t, pg, topic, "malformed-cleanup", statusPublished, &veryOld)
	if _, err := pg.Exec(ctx, `UPDATE outbox_events
SET payload='{"Action":"CONTAINER_CLEANUP","JobID":"invalid","ContainerCleanup":{"ContainerID":"container"}}'
WHERE topic=$1 AND event_key='malformed-cleanup'`, topic); err != nil {
		t.Fatal(err)
	}
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

	want := []string{"dead-old-timestamp", "failed-old-timestamp", "malformed-cleanup", "old-90m", "pending", "processing", "recent"}
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

	want = []string{"dead-old-timestamp", "failed-old-timestamp", "malformed-cleanup", "pending", "processing", "recent"}
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
	pg := testkit.Postgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
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

	if _, err := repo.CleanupPublishedEvents(canceled, time.Hour, 10); status.Code(err) != codes.Canceled {
		t.Fatalf("CleanupPublishedEvents with canceled context = %v, want Canceled", err)
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

// A separate database keeps global maintenance predicates from touching sibling fixtures.
func pruningRepository(t *testing.T) *Repository {
	t.Helper()
	admin := testkit.Postgres(t)
	dsn, err := url.Parse(testkit.PostgresDSN(t))
	require.NoError(t, err)
	name := "pruning_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(t.Context(), "CREATE DATABASE "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, dropErr := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		require.NoError(t, dropErr)
	})
	dsn.Path = "/" + name
	require.NoError(t, postgres.Migrate(dsn.String()))
	cfg, err := pgx.ParseConfig(dsn.String())
	require.NoError(t, err)
	pg, err := postgres.New(t.Context(), &postgres.Config{
		Host: cfg.Host, Port: int(cfg.Port), User: cfg.User, Password: cfg.Password,
		Database: name, MaxConns: 10, TLSConfig: &postgres.TLSConfig{},
	})
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	return New(&Config{}, pg, nil)
}

func seedPruningCleanup(t *testing.T, repo *Repository, completed bool) (eventID, intentID string) {
	t.Helper()
	intent, job := uuid.NewString(), uuid.NewString()
	_, err := repo.pg.Exec(t.Context(), `
INSERT INTO container_creation_intents(id,job_id,workflow_id,user_id,attempt,workflow_generation,
 process_instance_id,runtime_node_id,runtime_endpoint,runtime_instance,request_hash,name,log_retention)
VALUES($1::uuid,$2::uuid,uuidv7(),uuidv7(),1,1,uuidv7(),'node','tcp://runtime:2376','daemon','hash',$1::text,false)`, intent, job)
	require.NoError(t, err)
	_, err = repo.pg.Exec(t.Context(), `
INSERT INTO container_materializations(intent_id,container_id,runtime_instance) VALUES($1::uuid,$1::text,'daemon')`, intent)
	require.NoError(t, err)
	state := "READY"
	if completed {
		_, err = repo.pg.Exec(t.Context(), `INSERT INTO container_cleanup_receipts(job_id,container_id,runtime_instance,intent_id)
VALUES($1,$2::text,'daemon',$2::uuid)`, job, intent)
		require.NoError(t, err)
		state = "COMPLETED"
	}
	var id string
	err = repo.pg.QueryRow(t.Context(), `
INSERT INTO outbox_events(topic,kafka_key,event_key,payload,status,published_at,cleanup_state)
VALUES('workflows',$1,$1,jsonb_build_object('Action','CONTAINER_CLEANUP','JobID',$2::text,
 'ContainerCleanup',jsonb_build_object('ContainerID',$1::text,'RuntimeInstance','daemon','IntentID',$1::text)),
 'PUBLISHED',clock_timestamp()-interval '2 hours',$3) RETURNING id`, intent, job, state).Scan(&id)
	require.NoError(t, err)
	return id, intent
}

func TestIntegrationPruningBudgetFairnessAndProof(t *testing.T) {
	for _, preferred := range []bool{false, true} {
		for _, batch := range []int{1, 2, 3, 5, 10} {
			t.Run(fmt.Sprintf("cleanup=%v/batch=%d", preferred, batch), func(t *testing.T) {
				repo := pruningRepository(t)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				_, err := repo.pg.Exec(ctx, "UPDATE outbox_cleanup_turn SET next_cleanup=$1", preferred)
				require.NoError(t, err)
				old := time.Now().Add(-2 * time.Hour)
				for i := range 10 {
					seedOutboxRow(ctx, t, repo.pg, "ordinary", fmt.Sprint(i), statusPublished, &old)
					seedPruningCleanup(t, repo, true)
				}
				deleted, err := repo.CleanupPublishedEvents(ctx, time.Hour, batch)
				require.NoError(t, err)
				require.EqualValues(t, batch, deleted)
				var ordinary, cleanup int
				require.NoError(t, repo.pg.QueryRow(ctx, `SELECT count(*) FILTER(WHERE topic='ordinary'),
count(*) FILTER(WHERE topic='workflows') FROM outbox_events`).Scan(&ordinary, &cleanup))
				first := batch/2 + batch%2
				if preferred {
					require.Equal(t, 10-first, cleanup)
					require.Equal(t, 10-(batch-first), ordinary)
				} else {
					require.Equal(t, 10-first, ordinary)
					require.Equal(t, 10-(batch-first), cleanup)
				}
				var turn, publication bool
				require.NoError(t, repo.pg.QueryRow(ctx, "SELECT next_cleanup FROM outbox_cleanup_turn").Scan(&turn))
				require.Equal(t, !preferred, turn)
				require.NoError(t, repo.pg.QueryRow(ctx, "SELECT next_replay FROM outbox_relay_turns").Scan(&publication))
				require.False(t, publication)
			})
		}
	}
}

func TestIntegrationPruningBorrowExcludesMismatchesAndRevisitsProof(t *testing.T) {
	repo := pruningRepository(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	id, intent := seedPruningCleanup(t, repo, false)
	_, err := repo.pg.Exec(ctx, "UPDATE outbox_events SET cleanup_state='COMPLETED' WHERE id=$1", id)
	require.NoError(t, err)
	_, err = repo.pg.Exec(ctx, "UPDATE outbox_cleanup_turn SET next_cleanup=true")
	require.NoError(t, err)
	for range 4 {
		seedPruningCleanup(t, repo, true)
	}
	deleted, err := repo.CleanupPublishedEvents(ctx, time.Hour, 5)
	require.NoError(t, err)
	require.EqualValues(t, 4, deleted, "borrow must not charge the corrupt identity twice")
	var remaining int
	require.NoError(t, repo.pg.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&remaining))
	require.Equal(t, 1, remaining)
	_, err = repo.pg.Exec(ctx, `INSERT INTO container_cleanup_receipts(job_id,container_id,runtime_instance,intent_id)
SELECT job_id,$1::text,'daemon',id FROM container_creation_intents WHERE id=$1::uuid`, intent)
	require.NoError(t, err)
	deleted, err = repo.CleanupPublishedEvents(ctx, time.Hour, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted, "old timestamp must not hide a newly proven candidate")
}

func TestIntegrationPruningMalformedAndMismatchedIdentitiesFailClosed(t *testing.T) {
	for _, mutation := range []string{
		`jsonb_set(payload,'{JobID}','"invalid"')`,
		`jsonb_set(payload,'{JobID}',to_jsonb(uuidv7()::text))`,
		`jsonb_set(payload,'{ContainerCleanup,ContainerID}','"wrong"')`,
		`jsonb_set(payload,'{ContainerCleanup,RuntimeInstance}','"wrong"')`,
		`jsonb_set(payload,'{ContainerCleanup,IntentID}','"invalid"')`,
		`jsonb_set(payload,'{ContainerCleanup,IntentID}',to_jsonb(uuidv7()::text))`,
	} {
		t.Run(mutation, func(t *testing.T) {
			repo := pruningRepository(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			id, _ := seedPruningCleanup(t, repo, true)
			_, err := repo.pg.Exec(ctx, "UPDATE outbox_events SET payload="+mutation+" WHERE id=$1", id)
			require.NoError(t, err)
			old := time.Now().Add(-2 * time.Hour)
			seedOutboxRow(ctx, t, repo.pg, "ordinary", "survives-corrupt-prefix", statusPublished, &old)
			deleted, err := repo.CleanupPublishedEvents(ctx, time.Hour, 2)
			require.NoError(t, err)
			require.EqualValues(t, 1, deleted)
			var remaining int
			require.NoError(t, repo.pg.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE id=$1", id).Scan(&remaining))
			require.Equal(t, 1, remaining)
		})
	}
}

func TestIntegrationPruningReservationAndIndependentCommits(t *testing.T) {
	repo := pruningRepository(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	old := time.Now().Add(-2 * time.Hour)
	seedOutboxRow(ctx, t, repo.pg, "ordinary", "old", statusPublished, &old)
	seedPruningCleanup(t, repo, true)
	lock, err := repo.pg.BeginTx(ctx)
	require.NoError(t, err)
	_, err = lock.Exec(ctx, "SELECT * FROM outbox_cleanup_turn FOR UPDATE")
	require.NoError(t, err)
	n, err := repo.CleanupPublishedEvents(ctx, time.Hour, 2)
	require.NoError(t, err)
	require.Zero(t, n, "busy reservation must not execute a lane")
	require.NoError(t, lock.Rollback(ctx))
	_, err = repo.pg.Exec(ctx, `CREATE FUNCTION reject_cleanup_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF OLD.payload->>'Action'='CONTAINER_CLEANUP' THEN RAISE EXCEPTION 'injected cleanup failure'; END IF; RETURN OLD; END $$;
CREATE TRIGGER reject_cleanup_delete BEFORE DELETE ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_cleanup_delete()`)
	require.NoError(t, err)
	n, err = repo.CleanupPublishedEvents(ctx, time.Hour, 2)
	require.Equal(t, codes.Internal, status.Code(err))
	require.EqualValues(t, 1, n, "ordinary commit must survive cleanup rollback")
	var count int
	require.NoError(t, repo.pg.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&count))
	require.Equal(t, 1, count)
	_, err = repo.pg.Exec(ctx, "DELETE FROM outbox_cleanup_turn")
	require.NoError(t, err)
	n, err = repo.CleanupPublishedEvents(ctx, time.Hour, 2)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Zero(t, n)
}

func TestIntegrationPruningConcurrentCallsAndLockedCandidate(t *testing.T) {
	repo := pruningRepository(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	old := time.Now().Add(-2 * time.Hour)
	for i := range 10 {
		seedOutboxRow(ctx, t, repo.pg, "ordinary", fmt.Sprint(i), statusPublished, &old)
	}
	lock, err := repo.pg.BeginTx(ctx)
	require.NoError(t, err)
	_, err = lock.Exec(ctx, "SELECT id FROM outbox_events WHERE event_key='0' FOR UPDATE")
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan int64, 2)
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			n, callErr := repo.CleanupPublishedEvents(ctx, time.Hour, 5)
			results <- n
			errors <- callErr
		}()
	}
	close(start)
	var total int64
	for range 2 {
		total += <-results
		require.NoError(t, <-errors)
	}
	// A busy turn may legitimately skip one call; neither call can delete locked row.
	require.GreaterOrEqual(t, total, int64(5))
	require.LessOrEqual(t, total, int64(9))
	require.NoError(t, lock.Rollback(ctx))
	n, err := repo.CleanupPublishedEvents(ctx, time.Hour, 10)
	require.NoError(t, err)
	require.EqualValues(t, 10, total+n)
}

// Drop a confirmed COMMIT response at the wire boundary, after PostgreSQL accepted it.
func pruningCommitLoss(t *testing.T, repo *Repository, commit int32) *Repository {
	t.Helper()
	cfg, err := pgx.ParseConfig(testkit.PostgresDSN(t))
	require.NoError(t, err)
	var database string
	require.NoError(t, repo.pg.QueryRow(t.Context(), "SELECT current_database()").Scan(&database))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var seen atomic.Int32
	var workers sync.WaitGroup
	var mu sync.Mutex
	var connections []net.Conn
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			client, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			server, dialErr := net.DialTimeout("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), time.Second)
			if dialErr != nil {
				_ = client.Close()
				continue
			}
			mu.Lock()
			connections = append(connections, client, server)
			mu.Unlock()
			workers.Add(2)
			go func() {
				defer workers.Done()
				//nolint:errcheck // Fault proxy intentionally terminates connections.
				_, _ = io.Copy(server, client)
				_ = server.Close()
			}()
			go func() {
				defer workers.Done()
				defer client.Close()
				defer server.Close()
				for {
					var header [5]byte
					if _, readErr := io.ReadFull(server, header[:]); readErr != nil {
						return
					}
					length := int(binary.BigEndian.Uint32(header[1:])) - 4
					if length < 0 || length > 16<<20 {
						return
					}
					body := make([]byte, length)
					if _, readErr := io.ReadFull(server, body); readErr != nil {
						return
					}
					if header[0] == 'C' && string(body) == "COMMIT\x00" && seen.Add(1) == commit {
						return
					}
					if _, writeErr := client.Write(header[:]); writeErr != nil {
						return
					}
					if _, writeErr := client.Write(body); writeErr != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptDone
		mu.Lock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	address, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	port := address.Port
	pg, err := postgres.New(t.Context(), &postgres.Config{
		Host: "127.0.0.1", Port: port, User: cfg.User,
		Password: cfg.Password, Database: database, MaxConns: 3, TLSConfig: &postgres.TLSConfig{},
	})
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	return New(&Config{}, pg, nil)
}

func TestIntegrationPruningLostCommitResponses(t *testing.T) {
	for _, commit := range []int32{1, 2, 3} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			repo := pruningRepository(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			old := time.Now().Add(-2 * time.Hour)
			seedOutboxRow(ctx, t, repo.pg, "ordinary", "old", statusPublished, &old)
			seedPruningCleanup(t, repo, true)
			faulty := pruningCommitLoss(t, repo, commit)
			n, err := faulty.CleanupPublishedEvents(ctx, time.Hour, 2)
			require.Equal(t, codes.Unavailable, status.Code(err))
			var count int
			require.NoError(t, repo.pg.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&count))
			if commit == 1 {
				require.Zero(t, n, "uncertain reservation must not start lanes")
				require.Equal(t, 2, count)
			} else {
				require.EqualValues(t, 1, n, "only the other lane has confirmed commit")
				require.Zero(t, count, "both commits reached PostgreSQL")
			}
			var preference bool
			require.NoError(t, repo.pg.QueryRow(ctx, "SELECT next_cleanup FROM outbox_cleanup_turn").Scan(&preference))
			require.True(t, preference, "committed reservation survives lost response or failed lane")
		})
	}
}

func TestIntegrationPruningChildDeadlinePreservesOtherLane(t *testing.T) {
	repo := pruningRepository(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	old := time.Now().Add(-2 * time.Hour)
	seedOutboxRow(ctx, t, repo.pg, "ordinary", "old", statusPublished, &old)
	seedPruningCleanup(t, repo, true)
	_, err := repo.pg.Exec(ctx, `CREATE FUNCTION stall_cleanup_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF OLD.payload->>'Action'='CONTAINER_CLEANUP' THEN PERFORM pg_sleep(60); END IF; RETURN OLD; END $$;
CREATE TRIGGER stall_cleanup_delete BEFORE DELETE ON outbox_events FOR EACH ROW EXECUTE FUNCTION stall_cleanup_delete();
UPDATE outbox_cleanup_turn SET next_cleanup=true`)
	require.NoError(t, err)
	work, cancelWork := context.WithTimeout(ctx, 3*time.Second)
	defer cancelWork()
	n, err := repo.CleanupPublishedEvents(work, time.Hour, 2)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	require.EqualValues(t, 1, n, "cleanup child timeout must leave budget/time for ordinary")
	require.NoError(t, work.Err(), "child deadline must not exhaust parent")
	var count int
	require.NoError(t, repo.pg.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&count))
	require.Equal(t, 1, count, "canceled lane must have rolled back")
}

func TestPruningContextContract(t *testing.T) {
	repo := New(&Config{}, nil, nil)
	n, err := repo.CleanupPublishedEvents(context.Background(), 0, 1)
	require.NoError(t, err)
	require.Zero(t, n)
	n, err = repo.CleanupPublishedEvents(context.Background(), time.Hour, 1)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Zero(t, n)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = repo.CleanupPublishedEvents(ctx, time.Hour, 1)
	require.Equal(t, codes.Canceled, status.Code(err))
	ctx, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	_, err = repo.CleanupPublishedEvents(ctx, time.Hour, 1)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
}

func TestIntegrationPruningFailedLaneCannotBorrowOrCountUnconfirmedDeletes(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprintf("deferred=%v", deferred), func(t *testing.T) {
			repo := pruningRepository(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			old := time.Now().Add(-2 * time.Hour)
			for i := range 3 {
				seedOutboxRow(ctx, t, repo.pg, "ordinary", fmt.Sprint(i), statusPublished, &old)
			}
			seedPruningCleanup(t, repo, true)
			_, err := repo.pg.Exec(ctx, `CREATE SEQUENCE pruning_delete_calls;
CREATE FUNCTION reject_ordinary_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF OLD.topic='ordinary' THEN PERFORM nextval('pruning_delete_calls');
RAISE EXCEPTION 'injected ordinary failure'; END IF; RETURN OLD; END $$`)
			require.NoError(t, err)
			trigger := "CREATE TRIGGER reject_ordinary_delete BEFORE DELETE ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_ordinary_delete()"
			if deferred {
				trigger = "CREATE CONSTRAINT TRIGGER reject_ordinary_delete AFTER DELETE ON outbox_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_ordinary_delete()"
			}
			_, err = repo.pg.Exec(ctx, trigger)
			require.NoError(t, err)
			n, err := repo.CleanupPublishedEvents(ctx, time.Hour, 10)
			require.Equal(t, codes.Internal, status.Code(err))
			require.EqualValues(t, 1, n, "only confirmed cleanup commit counts, not pre-COMMIT deletes")
			var calls, remaining int
			require.NoError(t, repo.pg.QueryRow(ctx, "SELECT last_value FROM pruning_delete_calls").Scan(&calls))
			require.Equal(t, 1, calls, "failed preferred lane must not be retried by borrowing")
			require.NoError(t, repo.pg.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&remaining))
			require.Equal(t, 3, remaining)
		})
	}
}

func TestIntegrationPruningReceiptWriterAndMarkerInvariant(t *testing.T) {
	repo := pruningRepository(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	id, intent := seedPruningCleanup(t, repo, false)
	var job string
	require.NoError(t, repo.pg.QueryRow(ctx, "SELECT job_id FROM container_creation_intents WHERE id=$1", intent).Scan(&job))
	_, err := repo.pg.Exec(ctx, "UPDATE outbox_events SET event_key=$2 WHERE id=$1", id,
		"job:"+job+":container:"+intent+":runtime:daemon:cleanup")
	require.NoError(t, err)
	writer := jobsrepo.New(nil, nil, repo.pg, nil, nil, nil, nil)
	identity := jobsmodel.CleanupIdentity{JobID: job, ContainerID: intent, RuntimeInstance: "daemon", IntentID: intent}
	lock, err := repo.pg.BeginTx(ctx)
	require.NoError(t, err)
	_, err = lock.Exec(ctx, "SELECT id FROM outbox_events WHERE id=$1 FOR UPDATE", id)
	require.NoError(t, err)
	written := make(chan error, 1)
	go func() { written <- writer.RecordContainerCleanupReceipt(ctx, identity) }()
	// While the writer cannot publish COMPLETED, pruning cannot treat READY as proof.
	n, err := repo.CleanupPublishedEvents(ctx, time.Hour, 1)
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, lock.Rollback(ctx))
	require.NoError(t, <-written)
	var marker string
	require.NoError(t, repo.pg.QueryRow(ctx, "SELECT cleanup_state FROM outbox_events WHERE id=$1", id).Scan(&marker))
	require.Equal(t, "COMPLETED", marker)
	// Inject an unreachable marker violation: even valid receipt cannot bypass candidate policy.
	_, err = repo.pg.Exec(ctx, "UPDATE outbox_events SET cleanup_state=NULL WHERE id=$1", id)
	require.NoError(t, err)
	n, err = repo.CleanupPublishedEvents(ctx, time.Hour, 2)
	require.NoError(t, err)
	require.Zero(t, n)
	// Restore injected marker corruption before exercising the real completion writer.
	_, err = repo.pg.Exec(ctx, "UPDATE outbox_events SET cleanup_state='READY' WHERE id=$1", id)
	require.NoError(t, err)
	require.NoError(t, writer.RecordContainerCleanupReceipt(ctx, identity))
	n, err = repo.CleanupPublishedEvents(ctx, time.Hour, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.NoError(t, writer.RecordContainerCleanupReceipt(ctx, identity), "duplicate receipt after prune remains valid")
}

func TestIntegrationPruningReservationHasIndependentDeadline(t *testing.T) {
	repo := pruningRepository(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	old := time.Now().Add(-2 * time.Hour)
	seedOutboxRow(ctx, t, repo.pg, "ordinary", "old", statusPublished, &old)
	_, err := repo.pg.Exec(ctx, `CREATE FUNCTION stall_cleanup_turn() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_sleep(60);RETURN NEW;END $$;
CREATE TRIGGER stall_cleanup_turn BEFORE UPDATE ON outbox_cleanup_turn FOR EACH ROW EXECUTE FUNCTION stall_cleanup_turn()`)
	require.NoError(t, err)
	work, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	n, err := repo.CleanupPublishedEvents(work, time.Hour, 1)
	require.Zero(t, n)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	require.NoError(t, work.Err(), "reservation timeout must not consume the whole caller budget")
	var count int
	require.NoError(t, repo.pg.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&count))
	require.Equal(t, 1, count, "reservation failure must not start lanes")
	_, err = repo.pg.Exec(ctx, "DROP TRIGGER stall_cleanup_turn ON outbox_cleanup_turn")
	require.NoError(t, err)
	n, err = repo.CleanupPublishedEvents(ctx, time.Hour, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "next healthy call must recover")
}

func TestPruningFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		want    codes.Code
	}{
		{"query canceled", &pgconn.PgError{Code: "57014"}, codes.DeadlineExceeded},
		{"transport SQLSTATE", &pgconn.PgError{Code: "08006"}, codes.Unavailable},
		{"fatal database", &pgconn.PgError{Code: "57P01", Severity: "FATAL"}, codes.Unavailable},
		{"constraint", &pgconn.PgError{Code: "23514"}, codes.Internal},
		{"known commit rollback", pgx.ErrTxCommitRollback, codes.Internal},
		{"wrapped context", fmt.Errorf("query: %w", context.DeadlineExceeded), codes.DeadlineExceeded},
		{"wrapped cancellation", fmt.Errorf("query: %w", context.Canceled), codes.Canceled},
		{"connection lost", io.EOF, codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, status.Code(cleanupOperationError(t.Context(), tc.failure)))
		})
	}
	for _, tc := range []struct {
		name     string
		failures []error
		want     codes.Code
	}{
		{"none", nil, codes.OK},
		{"deadline beats SQL", []error{status.Error(codes.Internal, "SQL"), status.Error(codes.DeadlineExceeded, "budget")}, codes.DeadlineExceeded},
		{"transport beats deadline", []error{status.Error(codes.DeadlineExceeded, "budget"), status.Error(codes.Unavailable, "commit unknown")}, codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, status.Code(cleanupFailures(t.Context(), tc.failures))) })
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.Equal(t, codes.Canceled, status.Code(cleanupFailures(canceled, []error{status.Error(codes.Unavailable, "transport")})))
}
