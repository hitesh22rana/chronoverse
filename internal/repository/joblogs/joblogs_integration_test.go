//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package joblogs

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/meilisearch/meilisearch-go"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m,
		testkit.WithPostgres(),
		testkit.WithClickHouse(),
		testkit.WithRedis(),
		testkit.WithMeilisearch(),
		testkit.WithKafka(),
	)
}

// seedUserWorkflow inserts a user and a workflow; the joblogs pipeline only
// processes logs whose (workflow_id, user_id) pair exists.
func seedUserWorkflow(ctx context.Context, t *testing.T, pg *postgres.Postgres) (userID, workflowID string) {
	t.Helper()

	if err := pg.QueryRow(ctx, `
		INSERT INTO users (email, password)
		VALUES ($1, $2)
		RETURNING id
	`, fmt.Sprintf("joblogs-%s@chronoverse.test", t.Name()), "hash").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := pg.QueryRow(ctx, `
		INSERT INTO workflows (user_id, name, payload, kind, build_status, interval, log_retention)
		VALUES ($1, $2, '{}', 'CONTAINER', 'COMPLETED', 1, TRUE)
		RETURNING id
	`, userID, "joblogs-"+t.Name()).Scan(&workflowID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	return userID, workflowID
}

func TestIntegrationProcessesJobLogsEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pg := testkit.Postgres(t)
	ch := testkit.ClickHouse(t)
	ms := testkit.Meilisearch(t)
	producer := testkit.Kafka(t)

	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	jobID := "00000000-0000-0000-0000-0000000000d1"

	// Wire the consumer like the real service.
	lifecycle := kafkapkg.NewPartitionLifecycle()
	kfk, err := kafkapkg.New(ctx,
		kafkapkg.WithBrokers(testkit.KafkaSeed(t)),
		kafkapkg.WithConsumerGroup("joblogs-integration-"+t.Name()),
		kafkapkg.WithConsumeTopics(kafkapkg.TopicJobLogs),
		kafkapkg.WithDisableAutoCommit(),
		kafkapkg.WithPartitionLifecycle(lifecycle),
	)
	if err != nil {
		t.Fatalf("create kafka client: %v", err)
	}
	defer kfk.Close()

	repo := New(&Config{
		BatchJobLogsSizeLimit:    10,
		BatchJobLogsTimeInterval: 500 * time.Millisecond,
	}, testkit.Redis(t), pg, ch, ms, kfk, lifecycle)

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		//nolint:errcheck // Run blocks until ctx is canceled; the pipeline is asserted below.
		_ = repo.Run(ctx)
	}()

	// Produce three retained log events for the job.
	for i := uint32(1); i <= 3; i++ {
		record, err := joblogevents.KafkaRecord(&jobsmodel.JobLogEvent{
			EventKey:    fmt.Sprintf("log:%s:%s:%d", jobID, "stdout", i),
			JobID:       jobID,
			WorkflowID:  workflowID,
			UserID:      userID,
			Message:     fmt.Sprintf("log message %d", i),
			TimeStamp:   time.Now().UTC(),
			SequenceNum: i,
			Stream:      "stdout",
			Retention:   true,
		})
		if err != nil {
			t.Fatalf("KafkaRecord %d: %v", i, err)
		}
		if err := producer.ProduceSync(ctx, record).FirstErr(); err != nil {
			t.Fatalf("produce log %d: %v", i, err)
		}
	}

	// The logs land in ClickHouse.
	testkit.Eventually(t, 30*time.Second, 500*time.Millisecond, func() bool {
		return clickHouseRowCount(ctx, ch, jobID) == 3
	})

	// The logs are indexed in Meilisearch.
	index := ms.Index("job_logs")
	testkit.Eventually(t, 30*time.Second, 500*time.Millisecond, func() bool {
		res, err := index.Search("log message 1", &meilisearch.SearchRequest{
			Filter: fmt.Sprintf("job_id = %q", jobID),
			Limit:  1,
		})
		if err != nil {
			return false
		}
		return res.EstimatedTotalHits == 1
	})

	// Durable analytics counters are updated in PostgreSQL.
	testkit.Eventually(t, 30*time.Second, 500*time.Millisecond, func() bool {
		var logs int64
		if err := pg.QueryRow(ctx, `
			SELECT logs_count FROM analytics WHERE user_id = $1 AND workflow_id = $2
		`, userID, workflowID).Scan(&logs); err != nil {
			return false
		}
		return logs == 3
	})

	cancel()
	<-runDone
}

// clickHouseRowCount counts the job_logs rows for a job id, returning -1 on error.
func clickHouseRowCount(ctx context.Context, ch *clickhouse.Client, jobID string) int64 {
	rows, err := ch.Query(ctx, `SELECT count() FROM job_logs WHERE job_id = $1`, jobID)
	if err != nil {
		return -1
	}
	defer rows.Close()
	if !rows.Next() {
		return -1
	}
	var count int64
	if err := rows.Scan(&count); err != nil {
		return -1
	}
	return count
}

func TestIntegrationDropsLogsForUnknownWorkflows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pg := testkit.Postgres(t)
	ch := testkit.ClickHouse(t)
	producer := testkit.Kafka(t)

	// No workflow is seeded: logs referencing an unknown workflow must be dropped.
	jobID := "00000000-0000-0000-0000-0000000000d2"
	userID := "00000000-0000-0000-0000-0000000000e2"
	workflowID := "00000000-0000-0000-0000-0000000000f2"

	lifecycle := kafkapkg.NewPartitionLifecycle()
	kfk, err := kafkapkg.New(ctx,
		kafkapkg.WithBrokers(testkit.KafkaSeed(t)),
		kafkapkg.WithConsumerGroup("joblogs-integration-drop-"+t.Name()),
		kafkapkg.WithConsumeTopics(kafkapkg.TopicJobLogs),
		kafkapkg.WithDisableAutoCommit(),
		kafkapkg.WithPartitionLifecycle(lifecycle),
	)
	if err != nil {
		t.Fatalf("create kafka client: %v", err)
	}
	defer kfk.Close()

	repo := New(&Config{
		BatchJobLogsSizeLimit:    10,
		BatchJobLogsTimeInterval: 500 * time.Millisecond,
	}, testkit.Redis(t), pg, ch, testkit.Meilisearch(t), kfk, lifecycle)

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		//nolint:errcheck // Run blocks until ctx is canceled; the pipeline is asserted below.
		_ = repo.Run(ctx)
	}()

	record, err := joblogevents.KafkaRecord(&jobsmodel.JobLogEvent{
		EventKey:    "log:" + jobID + ":stdout:1",
		JobID:       jobID,
		WorkflowID:  workflowID,
		UserID:      userID,
		Message:     "orphan log",
		TimeStamp:   time.Now().UTC(),
		SequenceNum: 1,
		Stream:      "stdout",
		Retention:   true,
	})
	if err != nil {
		t.Fatalf("KafkaRecord: %v", err)
	}
	if err := producer.ProduceSync(ctx, record).FirstErr(); err != nil {
		t.Fatalf("produce log: %v", err)
	}

	// The batch is dropped before any durable write happens.
	time.Sleep(5 * time.Second)
	if count := clickHouseRowCount(ctx, ch, jobID); count != 0 {
		t.Fatalf("job_logs count = %d, want 0 for unknown workflow", count)
	}

	cancel()
	<-runDone
}
