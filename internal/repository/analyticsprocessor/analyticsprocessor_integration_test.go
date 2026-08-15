//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package analyticsprocessor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres(), testkit.WithKafka())
}

// newTestRepository builds an analyticsprocessor repository wired exactly like
// the analytics-processor service: a consumer group over the analytics topic
// with a partition lifecycle.
func newTestRepository(t *testing.T) *Repository {
	t.Helper()

	lifecycle := kafkapkg.NewPartitionLifecycle()
	kfk, err := kafkapkg.New(context.Background(),
		kafkapkg.WithBrokers(testkit.KafkaSeed(t)),
		kafkapkg.WithConsumerGroup("analyticsprocessor-integration-"+t.Name()),
		kafkapkg.WithConsumeTopics(kafkapkg.TopicAnalytics),
		kafkapkg.WithDisableAutoCommit(),
		kafkapkg.WithPartitionLifecycle(lifecycle),
	)
	if err != nil {
		t.Fatalf("create kafka client: %v", err)
	}
	t.Cleanup(kfk.Close)

	return New(testkit.Postgres(t), kfk, lifecycle)
}

func TestIntegrationProcessesAnalyticsEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	producer := testkit.Kafka(t)

	// Seed a user because analytics.user_id is a foreign key.
	userID := testkit.SeedUser(ctx, t, pg, fmt.Sprintf("analytics-%s@chronoverse.test", t.Name()))
	workflowID := "00000000-0000-0000-0000-0000000000c1"

	// Start the consumer pipeline.
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		//nolint:errcheck // Run blocks until ctx is canceled; the pipeline is asserted below.
		_ = repo.Run(ctx)
	}()

	// Produce one event of each type.
	events := [][]byte{
		mustAnalyticEvent(t, "", userID, workflowID, analyticsmodel.EventTypeWorkflows, analyticsmodel.EventTypeWorkflowsData{Kind: "CONTAINER"}),
		mustAnalyticEvent(t, "", userID, workflowID, analyticsmodel.EventTypeJobs, analyticsmodel.EventTypeJobsData{JobExecutionDuration: 42}),
		mustAnalyticEvent(t, "", userID, workflowID, analyticsmodel.EventTypeLogs, analyticsmodel.EventTypeLogsData{Count: 7}),
	}
	for _, event := range events {
		if err := producer.ProduceSync(ctx, &kgo.Record{Topic: kafkapkg.TopicAnalytics, Value: event}).FirstErr(); err != nil {
			t.Fatalf("produce analytics event: %v", err)
		}
	}

	// The analytics row aggregates the three events.
	testkit.Eventually(t, 20*time.Second, 200*time.Millisecond, func() bool {
		var kind string
		var jobs, logs, duration int64
		if err := pg.QueryRow(ctx, `
			SELECT kind, jobs_count, logs_count, total_job_execution_duration
			FROM analytics WHERE user_id = $1 AND workflow_id = $2
		`, userID, workflowID).Scan(&kind, &jobs, &logs, &duration); err != nil {
			return false
		}
		return kind == "CONTAINER" && jobs == 1 && logs == 7 && duration == 42
	})

	cancel()
	<-runDone
}

func TestIntegrationDeduplicatesEventsByKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	producer := testkit.Kafka(t)

	userID := testkit.SeedUser(ctx, t, pg, fmt.Sprintf("analytics-%s@chronoverse.test", t.Name()))
	workflowID := "00000000-0000-0000-0000-0000000000c2"

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		//nolint:errcheck // Run blocks until ctx is canceled; the pipeline is asserted below.
		_ = repo.Run(ctx)
	}()

	// The same event (same key) is produced twice; it must be processed once.
	event := mustAnalyticEvent(t, "dedup-"+t.Name(), userID, workflowID, analyticsmodel.EventTypeLogs, analyticsmodel.EventTypeLogsData{Count: 3})
	for range 2 {
		if err := producer.ProduceSync(ctx, &kgo.Record{Topic: kafkapkg.TopicAnalytics, Value: event}).FirstErr(); err != nil {
			t.Fatalf("produce analytics event: %v", err)
		}
	}

	testkit.Eventually(t, 20*time.Second, 200*time.Millisecond, func() bool {
		var logs int64
		if err := pg.QueryRow(ctx, `
			SELECT logs_count FROM analytics WHERE user_id = $1 AND workflow_id = $2
		`, userID, workflowID).Scan(&logs); err != nil {
			return false
		}
		return logs == 3
	})

	// Give the second (duplicate) record a chance to be processed, then confirm
	// it was not double counted.
	time.Sleep(2 * time.Second)
	var logs int64
	if err := pg.QueryRow(ctx, `
		SELECT logs_count FROM analytics WHERE user_id = $1 AND workflow_id = $2
	`, userID, workflowID).Scan(&logs); err != nil {
		t.Fatalf("fetch logs_count: %v", err)
	}
	if logs != 3 {
		t.Fatalf("logs_count = %d, want 3 (duplicate event must be ignored)", logs)
	}

	cancel()
	<-runDone
}

func mustAnalyticEvent(t *testing.T, eventKey, userID, workflowID string, eventType analyticsmodel.EventType, data any) []byte {
	t.Helper()

	bytes, err := analyticsmodel.NewAnalyticEventBytesWithKey(eventKey, userID, workflowID, eventType, data)
	if err != nil {
		t.Fatalf("NewAnalyticEventBytesWithKey: %v", err)
	}
	return bytes
}
