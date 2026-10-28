//nolint:testpackage // Exercises the batch boundary with real storage services.
package joblogs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meilisearch/meilisearch-go"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestProcessLogsBatchEmpty(t *testing.T) {
	repo := &Repository{tp: otel.Tracer("joblogs-test")}
	got, err := repo.processLogsBatch(t.Context(), nil)
	if err != nil || got != nil {
		t.Fatalf("empty batch = %v, %v; want nil, nil", got, err)
	}
}

func batchContractRepository(t *testing.T) *Repository {
	t.Helper()
	return &Repository{tp: otel.Tracer("joblogs-test"), pg: testkit.Postgres(t), ch: testkit.ClickHouse(t), ms: testkit.Meilisearch(t)}
}

func batchContractEvent(t *testing.T, repo *Repository, retention bool) *queueData {
	t.Helper()
	userID := testkit.SeedUser(t.Context(), t, repo.pg, "joblogs-"+uuid.NewString()+"@chronoverse.test")
	workflowID := testkit.SeedWorkflow(t.Context(), t, repo.pg, userID, "joblogs-"+t.Name())
	return &queueData{logEntry: &jobsmodel.JobLogEvent{
		JobID: uuid.NewString(), WorkflowID: workflowID, UserID: userID,
		Stream: "stdout", SequenceNum: 1, Message: "batch contract " + t.Name(),
		TimeStamp: time.Now().UTC().Truncate(time.Millisecond), Retention: retention,
	}}
}

func assertBatchAnalytics(t *testing.T, repo *Repository, item *queueData, want int64) {
	t.Helper()
	var count int64
	if err := repo.pg.QueryRow(t.Context(),
		`SELECT COALESCE(sum(logs_count), 0) FROM analytics WHERE user_id=$1 AND workflow_id=$2`,
		item.logEntry.UserID, item.logEntry.WorkflowID).Scan(&count); err != nil {
		t.Fatalf("read analytics: %v", err)
	}
	if count != want {
		t.Fatalf("analytics logs_count = %d, want %d", count, want)
	}
	if count = batchProcessedIdentityCount(t, repo, item); count != want {
		t.Fatalf("processed identity count = %d, want %d", count, want)
	}
}

func batchProcessedIdentityCount(t *testing.T, repo *Repository, item *queueData) int64 {
	t.Helper()
	key := item.logEntry.EventKey
	if key == "" {
		key = idempotency.LogEventKey(item.logEntry.JobID, item.logEntry.Stream, item.logEntry.SequenceNum)
	}
	var count int64
	if err := repo.pg.QueryRow(t.Context(), `SELECT count(*) FROM processed_events WHERE consumer=$1 AND event_key=$2`, logAnalyticsConsumer, key).Scan(&count); err != nil {
		t.Fatalf("read processed identity: %v", err)
	}
	return count
}

func TestIntegrationBatchProcessedIdentityAssertion(t *testing.T) {
	repo := &Repository{pg: testkit.Postgres(t)}
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-%t", explicit), func(t *testing.T) {
			item := &queueData{logEntry: &jobsmodel.JobLogEvent{JobID: uuid.NewString(), Stream: "stderr", SequenceNum: 7}}
			key := idempotency.LogEventKey(item.logEntry.JobID, item.logEntry.Stream, item.logEntry.SequenceNum)
			if explicit {
				key = "explicit-" + uuid.NewString()
				item.logEntry.EventKey = key
			}
			original := item.logEntry.EventKey
			if got := batchProcessedIdentityCount(t, repo, item); got != 0 {
				t.Fatalf("unprocessed identity count = %d, want 0", got)
			}
			if _, err := repo.pg.Exec(t.Context(), `INSERT INTO processed_events (consumer,event_key) VALUES ($1,$2)`, logAnalyticsConsumer, key); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := repo.pg.Exec(context.Background(), `DELETE FROM processed_events WHERE consumer=$1 AND event_key=$2`, logAnalyticsConsumer, key); err != nil {
					t.Errorf("remove seeded processed identity: %v", err)
				}
			})
			if got := batchProcessedIdentityCount(t, repo, item); got != 1 {
				t.Fatalf("seeded identity count = %d, want 1", got)
			}
			if item.logEntry.EventKey != original {
				t.Fatalf("assertion changed event key from %q to %q", original, item.logEntry.EventKey)
			}
		})
	}
}

func assertBatchStorage(t *testing.T, repo *Repository, item *queueData, want int64) {
	t.Helper()
	if got := clickHouseRowCount(t.Context(), repo.ch, item.logEntry.JobID); got != want {
		t.Fatalf("ClickHouse rows = %d, want %d", got, want)
	}
	result, err := repo.ms.Index("job_logs").SearchWithContext(t.Context(), "", &meilisearch.SearchRequest{
		Filter: fmt.Sprintf("job_id = %q", item.logEntry.JobID), Limit: 10,
	})
	if err != nil {
		t.Fatalf("search retained logs: %v", err)
	}
	if int64(len(result.Hits)) != want {
		t.Fatalf("Meili hits = %d, want %d", len(result.Hits), want)
	}
}

func TestIntegrationBatchRetentionAndReplay(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("retained-%t", retained), func(t *testing.T) {
			repo := batchContractRepository(t)
			item := batchContractEvent(t, repo, retained)
			duplicateLog := *item.logEntry
			duplicate := &queueData{logEntry: &duplicateLog}
			for attempt := range 2 {
				got, err := repo.processLogsBatch(t.Context(), []*queueData{item, duplicate})
				if err != nil || len(got) != 2 || got[0] != item || got[1] != duplicate {
					t.Fatalf("attempt %d returned %v, %v", attempt, got, err)
				}
				expectedKey := fmt.Sprintf("log:%s:stdout:1", item.logEntry.JobID)
				if item.logEntry.EventKey != expectedKey || duplicate.logEntry.EventKey != expectedKey {
					t.Fatalf("event key = %q, want %q", item.logEntry.EventKey, expectedKey)
				}
				assertBatchAnalytics(t, repo, item, 1)
			}
			// ReplacingMergeTree may retain physical replay copies until merges finish.
			if retained {
				if err := repo.ch.Exec(t.Context(), "OPTIMIZE TABLE job_logs FINAL"); err != nil {
					t.Fatalf("merge replay copies: %v", err)
				}
				assertBatchStorage(t, repo, item, 1)
			} else {
				assertBatchStorage(t, repo, item, 0)
			}
		})
	}
}

func TestIntegrationBatchInactiveWorkflowCleanup(t *testing.T) {
	repo := batchContractRepository(t)
	item := batchContractEvent(t, repo, true)
	unrelated := seedUnrelatedBatchLog(t, repo)
	if _, err := repo.processLogsBatch(t.Context(), []*queueData{item}); err != nil {
		t.Fatalf("seed retained logs: %v", err)
	}
	if _, err := repo.pg.Exec(t.Context(), "DELETE FROM workflows WHERE id=$1", item.logEntry.WorkflowID); err != nil {
		t.Fatalf("delete workflow: %v", err)
	}
	got, err := repo.processLogsBatch(t.Context(), []*queueData{item})
	if err != nil || got != nil {
		t.Fatalf("inactive batch = %v, %v", got, err)
	}
	assertBatchStorage(t, repo, item, 0)
	assertBatchAnalytics(t, repo, item, 1)
	assertBatchStorage(t, repo, unrelated, 1)
	assertBatchAnalytics(t, repo, unrelated, 1)
}

func TestIntegrationPublishLogGuardsAndDelivery(t *testing.T) {
	rdb := testkit.Redis(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	event := &jobsmodel.JobLogEvent{
		EventKey: "log:live:stdout:1", JobID: uuid.NewString(), Retention: true,
		WorkflowID: uuid.NewString(), UserID: uuid.NewString(),
		Stream: "stdout", SequenceNum: 1, Message: "eligible",
		TimeStamp: time.Now().UTC().Truncate(time.Millisecond),
	}
	want := *event
	want.LivePublished = true
	sub := rdb.Subscribe(ctx, redis.GetJobLogsChannel(event.JobID))
	t.Cleanup(func() {
		if err := sub.Close(); err != nil {
			t.Errorf("close live subscription: %v", err)
		}
	})
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe to live logs: %v", err)
	}
	repo := &Repository{}
	repo.publishLog(ctx, event)
	repo.livePublisher = joblogevents.NewLivePublisher(rdb, joblogevents.LivePublisherConfig{BufferSize: 1})
	repo.publishLog(ctx, nil)
	for _, skip := range []jobsmodel.JobLogEvent{
		{JobID: event.JobID, EventKey: "unretained", Retention: false},
		{JobID: event.JobID, EventKey: "already-published", Retention: true, LivePublished: true},
	} {
		repo.publishLog(ctx, &skip)
	}
	repo.publishLog(ctx, event)
	repo.publishLog(ctx, &jobsmodel.JobLogEvent{JobID: event.JobID, EventKey: "overflow", Retention: true})
	// Starting after enqueueing makes both skip guards and overflow observable in a one-slot queue.
	done := repo.runLiveJobLogPublisher(ctx, nil)
	defer func() {
		cancel()
		<-done
	}()
	message, err := sub.ReceiveMessage(ctx)
	if err != nil {
		t.Fatalf("receive eligible log: %v", err)
	}
	var got jobsmodel.JobLogEvent
	if err := json.Unmarshal([]byte(message.Payload), &got); err != nil {
		t.Fatalf("decode live log: %v", err)
	}
	if got != want || event.LivePublished {
		t.Fatalf("live log = %+v, original = %+v; want %+v and an unchanged original", got, event, want)
	}
}

func seedUnrelatedBatchLog(t *testing.T, repo *Repository) *queueData {
	t.Helper()
	item := batchContractEvent(t, repo, true)
	if _, err := repo.processLogsBatch(t.Context(), []*queueData{item}); err != nil {
		t.Fatalf("seed retained logs: %v", err)
	}
	return item
}

func unavailableBatchMeili(t *testing.T) meilisearch.ServiceManager {
	t.Helper()
	server := httptest.NewServer(nil)
	server.Close()
	return meilisearch.New(server.URL)
}

func isolatedBatchClickHouse(t *testing.T) *clickhouse.Client {
	t.Helper()
	client, err := clickhouse.New(t.Context(), &clickhouse.Config{
		Hosts: []string{testkit.ClickHouseAddr(t)}, Database: "chronoverse",
		Username: "chronoverse-client", Password: "chronoverse", MaxOpenConns: 2, MaxIdleConns: 1,
		DialTimeout: time.Second, TLSConfig: &clickhouse.TLSConfig{},
	})
	if err != nil {
		t.Fatalf("isolated ClickHouse client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close isolated ClickHouse: %v", err)
		}
	})
	return client
}

func assertBatchFailure(t *testing.T, got []*queueData, err error, message string) {
	t.Helper()
	if got != nil || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), message) {
		t.Fatalf("batch = %v, %v; want nil, Internal containing %q", got, err, message)
	}
}

func TestIntegrationBatchClickHouseFailureStopsProcessing(t *testing.T) {
	repo := batchContractRepository(t)
	item := batchContractEvent(t, repo, true)
	healthyCH := repo.ch
	repo.ch = isolatedBatchClickHouse(t)
	if err := repo.ch.Close(); err != nil {
		t.Fatalf("close ClickHouse: %v", err)
	}
	got, err := repo.processLogsBatch(t.Context(), []*queueData{item})
	assertBatchFailure(t, got, err, "failed to prepare batch")
	repo.ch = healthyCH
	assertBatchStorage(t, repo, item, 0)
	assertBatchAnalytics(t, repo, item, 0)
	if _, err := repo.processLogsBatch(t.Context(), []*queueData{item}); err != nil {
		t.Fatalf("retry after storage recovery: %v", err)
	}
	assertBatchStorage(t, repo, item, 1)
	assertBatchAnalytics(t, repo, item, 1)
}

func TestIntegrationBatchMeiliFailureRetriesWithoutDoubleCounting(t *testing.T) {
	repo := batchContractRepository(t)
	item := batchContractEvent(t, repo, true)
	healthyMS := repo.ms
	repo.ms = unavailableBatchMeili(t)
	got, err := repo.processLogsBatch(t.Context(), []*queueData{item})
	assertBatchFailure(t, got, err, "failed to add documents")
	if count := clickHouseRowCount(t.Context(), repo.ch, item.logEntry.JobID); count != 1 {
		t.Fatalf("durable first-stage rows = %d, want 1", count)
	}
	assertBatchAnalytics(t, repo, item, 0)
	repo.ms = healthyMS
	if _, err := repo.processLogsBatch(t.Context(), []*queueData{item}); err != nil {
		t.Fatalf("retry after Meili recovery: %v", err)
	}
	if err := repo.ch.Exec(t.Context(), "OPTIMIZE TABLE job_logs FINAL"); err != nil {
		t.Fatalf("merge replay copies: %v", err)
	}
	assertBatchStorage(t, repo, item, 1)
	assertBatchAnalytics(t, repo, item, 1)
}

// afterBatchIndexing runs once after real indexing completes, before the second workflow check.
type afterBatchIndexing struct {
	meilisearch.ServiceManager
	after func()
}

func (client *afterBatchIndexing) WaitForTaskWithContext(ctx context.Context, uid int64, interval time.Duration) (*meilisearch.Task, error) {
	task, err := client.ServiceManager.WaitForTaskWithContext(ctx, uid, interval)
	if err == nil && task.Status == meilisearch.TaskStatusSucceeded && client.after != nil {
		after := client.after
		client.after = nil
		after()
	}
	return task, err
}

func TestIntegrationBatchDeletionDuringIndexingRemovesRetainedLogs(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("mixed-%t", mixed), func(t *testing.T) {
			repo := batchContractRepository(t)
			item := batchContractEvent(t, repo, true)
			unrelated := batchContractEvent(t, repo, true)
			batch := []*queueData{item}
			if mixed {
				batch = append(batch, unrelated)
			} else if _, err := repo.processLogsBatch(t.Context(), []*queueData{unrelated}); err != nil {
				t.Fatalf("seed unrelated log: %v", err)
			}
			repo.ms = &afterBatchIndexing{ServiceManager: repo.ms, after: func() {
				if _, err := repo.pg.Exec(t.Context(), "DELETE FROM workflows WHERE id=$1", item.logEntry.WorkflowID); err != nil {
					t.Fatalf("delete indexed workflow: %v", err)
				}
			}}
			got, err := repo.processLogsBatch(t.Context(), batch)
			if err != nil {
				t.Fatalf("deleted batch failed: %v", err)
			}
			if mixed {
				if len(got) != 1 || got[0] != unrelated {
					t.Fatalf("deleted batch = %v; want only the active workflow's log", got)
				}
			} else if got != nil {
				t.Fatalf("deleted batch = %v; want nil", got)
			}
			assertBatchStorage(t, repo, item, 0)
			assertBatchAnalytics(t, repo, item, 0)
			assertBatchStorage(t, repo, unrelated, 1)
			assertBatchAnalytics(t, repo, unrelated, 1)
		})
	}
}

func isolatedBatchPostgres(t *testing.T) *postgres.Postgres {
	t.Helper()
	config, err := pgxpool.ParseConfig(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatalf("parse PostgreSQL DSN: %v", err)
	}
	c := config.ConnConfig
	pg, err := postgres.New(t.Context(), &postgres.Config{
		Host: c.Host, Port: int(c.Port), User: c.User, Password: c.Password, Database: c.Database,
		MaxConns: 2, DialTimeout: time.Second, TLSConfig: &postgres.TLSConfig{},
	})
	if err != nil {
		t.Fatalf("isolated PostgreSQL client: %v", err)
	}
	t.Cleanup(pg.Close)
	return pg
}

func TestIntegrationBatchSecondWorkflowCheckFailure(t *testing.T) {
	repo := batchContractRepository(t)
	item := batchContractEvent(t, repo, true)
	healthyPG := repo.pg
	repo.pg = isolatedBatchPostgres(t)
	repo.ms = &afterBatchIndexing{ServiceManager: repo.ms, after: repo.pg.Close}
	got, err := repo.processLogsBatch(t.Context(), []*queueData{item})
	assertBatchFailure(t, got, err, "failed to fetch active workflows")
	repo.pg = healthyPG
	assertBatchStorage(t, repo, item, 1)
	assertBatchAnalytics(t, repo, item, 0)
	if _, err := repo.processLogsBatch(t.Context(), []*queueData{item}); err != nil {
		t.Fatalf("retry after PostgreSQL recovery: %v", err)
	}
	assertBatchAnalytics(t, repo, item, 1)
}

func TestIntegrationBatchDeletedCleanupFailureIsReturned(t *testing.T) {
	for _, duringIndexing := range []bool{false, true} {
		t.Run(fmt.Sprintf("during-indexing-%t", duringIndexing), func(t *testing.T) {
			repo := batchContractRepository(t)
			item := batchContractEvent(t, repo, true)
			unrelated := seedUnrelatedBatchLog(t, repo)
			healthyCH := repo.ch
			repo.ch = isolatedBatchClickHouse(t)
			removeAndClose := func() {
				if _, err := repo.pg.Exec(t.Context(), "DELETE FROM workflows WHERE id=$1", item.logEntry.WorkflowID); err != nil {
					t.Fatalf("delete workflow: %v", err)
				}
				if err := repo.ch.Close(); err != nil {
					t.Fatalf("close ClickHouse: %v", err)
				}
			}
			if duringIndexing {
				repo.ms = &afterBatchIndexing{ServiceManager: repo.ms, after: removeAndClose}
			} else {
				removeAndClose()
			}
			got, err := repo.processLogsBatch(t.Context(), []*queueData{item})
			assertBatchFailure(t, got, err, "failed to delete skipped ClickHouse logs")
			repo.ch = healthyCH
			assertBatchAnalytics(t, repo, item, 0)
			if _, err := repo.processLogsBatch(t.Context(), []*queueData{item}); err != nil {
				t.Fatalf("retry deleted cleanup: %v", err)
			}
			assertBatchStorage(t, repo, item, 0)
			assertBatchStorage(t, repo, unrelated, 1)
			assertBatchAnalytics(t, repo, unrelated, 1)
		})
	}
}

func TestIntegrationBatchAnalyticsFailureRollsBackReplayIdentity(t *testing.T) {
	repo := batchContractRepository(t)
	item := batchContractEvent(t, repo, true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := repo.pg.Exec(ctx, `DROP TRIGGER IF EXISTS reject_batch_analytics ON analytics; DROP FUNCTION IF EXISTS reject_batch_analytics()`); err != nil {
			t.Errorf("remove analytics failure: %v", err)
		}
	})
	if _, err := repo.pg.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION reject_batch_analytics() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
 IF NEW.workflow_id = '%s'::uuid THEN RAISE EXCEPTION 'analytics storage unavailable'; END IF;
 RETURN NEW; END $$;
 CREATE TRIGGER reject_batch_analytics BEFORE INSERT OR UPDATE ON analytics FOR EACH ROW EXECUTE FUNCTION reject_batch_analytics()`, item.logEntry.WorkflowID)); err != nil {
		t.Fatalf("install analytics failure: %v", err)
	}
	got, err := repo.processLogsBatch(t.Context(), []*queueData{item})
	assertBatchFailure(t, got, err, "failed to update logs analytics")
	assertBatchStorage(t, repo, item, 1)
	assertBatchAnalytics(t, repo, item, 0)
	if _, err := repo.pg.Exec(t.Context(), `DROP TRIGGER reject_batch_analytics ON analytics; DROP FUNCTION reject_batch_analytics()`); err != nil {
		t.Fatalf("recover analytics: %v", err)
	}
	if _, err := repo.processLogsBatch(t.Context(), []*queueData{item}); err != nil {
		t.Fatalf("retry after analytics recovery: %v", err)
	}
	assertBatchAnalytics(t, repo, item, 1)
}

func TestIntegrationProcessRecordsBatchInitialWorkflowCheckFailure(t *testing.T) {
	repo := batchContractRepository(t)
	item := batchContractEvent(t, repo, true)
	payload, err := json.Marshal(item.logEntry)
	if err != nil {
		t.Fatal(err)
	}
	healthyPG := repo.pg
	repo.pg = isolatedBatchPostgres(t)
	repo.pg.Close()
	err = repo.processRecordsBatch(t.Context(), []*kgo.Record{{Value: payload}})
	if status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), "failed to fetch active workflows") {
		t.Fatalf("process records = %v, want Internal containing failed to fetch active workflows", err)
	}
	repo.pg = healthyPG
	assertBatchStorage(t, repo, item, 0)
	assertBatchAnalytics(t, repo, item, 0)
}

func TestIntegrationBatchClickHouseAppendFailureIsReturned(t *testing.T) {
	repo := &Repository{tp: otel.Tracer("joblogs-test"), ch: testkit.ClickHouse(t)}
	err := repo.insertLogsBatchToClickhouse(t.Context(), []*jobsmodel.JobLogEvent{{
		EventKey: "log:append:stdout:1", JobID: "not-a-uuid",
		WorkflowID: uuid.NewString(), UserID: uuid.NewString(),
		TimeStamp: time.Now().UTC(), Message: "append failure", SequenceNum: 1, Stream: "stdout",
	}})
	if status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), "failed to prepare batch") {
		t.Fatalf("append failure = %v, want Internal containing failed to prepare batch", err)
	}
}

func TestIntegrationFilterActiveWorkflowLogsSkipsMalformedEntries(t *testing.T) {
	repo := batchContractRepository(t)
	item := batchContractEvent(t, repo, true)
	active, skipped, err := repo.filterActiveWorkflowLogs(t.Context(), []*queueData{
		nil,
		{},
		{logEntry: &jobsmodel.JobLogEvent{WorkflowID: "not-a-workflow", UserID: "not-a-user"}},
		item,
	})
	if err != nil || len(active) != 1 || active[0] != item || len(skipped) != 0 {
		t.Fatalf("filter = %v, %v, %v; want [item], [], nil", active, skipped, err)
	}
}

func TestIntegrationFetchActiveWorkflowLogPairsReportsInvalidIDs(t *testing.T) {
	repo := &Repository{tp: otel.Tracer("joblogs-test"), pg: testkit.Postgres(t)}
	_, err := repo.fetchActiveWorkflowLogPairs(t.Context(), []workflowLogPair{{
		workflowID: "not-a-workflow", userID: uuid.NewString(),
	}})
	if status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), "failed to read active workflows") {
		t.Fatalf("invalid pairs = %v, want Internal read failure", err)
	}
}

func TestIntegrationProcessLogsAnalyticsBeginTransactionFailure(t *testing.T) {
	pg := isolatedBatchPostgres(t)
	pg.Close()
	repo := &Repository{tp: otel.Tracer("joblogs-test"), pg: pg}
	err := repo.processLogsAnalytics(t.Context(), nil)
	if status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), "failed to start log analytics transaction") {
		t.Fatalf("begin failure = %v, want Internal", err)
	}
}

func TestIntegrationProcessLogsAnalyticsCommitFailure(t *testing.T) {
	repo := &Repository{tp: otel.Tracer("joblogs-test"), pg: testkit.Postgres(t)}
	item := batchContractEvent(t, repo, false)
	const triggerName = "reject_batch_analytics_commit"
	cleanup := func() {
		if _, err := repo.pg.Exec(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON analytics; DROP FUNCTION IF EXISTS %s()", triggerName, triggerName,
		)); err != nil {
			t.Errorf("remove analytics commit trigger: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := repo.pg.Exec(t.Context(), fmt.Sprintf(`
		CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.workflow_id = '%[2]s'::uuid THEN RAISE EXCEPTION 'analytics commit unavailable'; END IF;
			RETURN NEW;
		END $$;
		CREATE CONSTRAINT TRIGGER %[1]s AFTER INSERT OR UPDATE ON analytics
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION %[1]s();`,
		triggerName, item.logEntry.WorkflowID,
	)); err != nil {
		t.Fatalf("install commit failure: %v", err)
	}
	err := repo.processLogsAnalytics(t.Context(), []*queueData{item})
	if status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), "failed to commit log analytics transaction") {
		t.Fatalf("commit failure = %v, want Internal", err)
	}
	assertBatchAnalytics(t, repo, item, 0)
}
