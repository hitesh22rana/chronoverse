//nolint:testpackage // Verifies executor flush behavior against durable Kafka output.
package executor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationContainerLogsFlushBeforeStreamCloses(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cfg := defaultConfig()
	cfg.JobLogBatchSize = 10
	cfg.JobLogBatchInterval = 5 * time.Millisecond
	// An unset retry budget must still attempt durable publication once.
	cfg.JobLogPublishRetries = 0
	repo := &Repository{cfg: cfg, kfk: testkit.Kafka(t)}
	jobID := uuid.NewString()
	consumer := testkit.KafkaRecordConsumer(t, kafkapkg.TopicJobLogs)
	workflow := containerWorkflow()
	workflow.LogRetention = false
	logs := make(chan *jobsmodel.JobLog, 2)
	logs <- &jobsmodel.JobLog{Stream: "stdout", SequenceNum: 1, Timestamp: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)}
	stamp := time.Now().UTC()
	logs <- &jobsmodel.JobLog{Stream: "stdout", SequenceNum: 2, Timestamp: stamp, Message: "durable without live retention"}
	done := make(chan struct{})
	stopped := repo.publishContainerLogs(ctx, jobID, 3, workflow, logs, done)
	defer func() {
		close(done)
		select {
		case <-stopped:
		case <-ctx.Done():
			t.Error("publisher did not stop")
		}
	}()
	record := nextReplayRecord(ctx, t, consumer, jobID)
	var event jobsmodel.JobLogEvent
	if err := json.Unmarshal(record.Value, &event); err != nil {
		t.Fatal(err)
	}
	if event.JobID != jobID || event.WorkflowID != workflow.GetId() || event.SequenceNum != 2 ||
		event.Message != "durable without live retention" || event.Retention || event.LivePublished || !event.TimeStamp.Equal(stamp) {
		t.Fatalf("flushed event=%+v", event)
	}
	// The source remains open and the batch is below capacity, so this publication
	// proves the timer flushed it rather than a close or full-batch boundary.
	select {
	case <-stopped:
		t.Fatal("publisher stopped while stream open")
	default:
	}
}
