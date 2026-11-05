//nolint:testpackage // Verifies replayed executor records with real Kafka and Redis.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationReplayedContainerLogsPreserveDurableIdentity(t *testing.T) {
	producer := testkit.Kafka(t)
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("retention_%t", retained), func(t *testing.T) {
			consumer := testkit.KafkaRecordConsumer(t, kafkapkg.TopicJobLogs)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			claim, workflow := replayContractIdentity()
			claim.Id = uuid.NewString()
			workflow.LogRetention = retained
			repo := &Repository{cfg: defaultConfig(), kfk: producer}
			stamp := time.Date(2026, time.January, 2, 3, 4, 5, 123456789, time.UTC)
			for range 2 {
				logs := make(chan *jobsmodel.JobLog, 2)
				errs := make(chan error)
				logs <- &jobsmodel.JobLog{Message: "restored stdout", Timestamp: stamp, Stream: "stdout", SequenceNum: 1}
				logs <- &jobsmodel.JobLog{Message: "restored stderr", Timestamp: stamp.Add(time.Second), Stream: "stderr", SequenceNum: 1}
				close(logs)
				close(errs)
				container := &replayContractContainer{logs: logs, errs: errs}
				if err := repo.replayContainerLogs(ctx, container, claim, workflow, "recovered-container"); err != nil {
					t.Fatalf("replay: %v", err)
				}
				for _, stream := range []string{"stdout", "stderr"} {
					record := nextReplayRecord(ctx, t, consumer, claim.GetId())
					var event jobsmodel.JobLogEvent
					if err := json.Unmarshal(record.Value, &event); err != nil {
						t.Fatal(err)
					}
					wantTime := stamp
					if stream == "stderr" {
						wantTime = stamp.Add(time.Second)
					}
					wantKey := fmt.Sprintf("log:%s:attempt:2:%s:1", claim.GetId(), stream)
					want := jobsmodel.JobLogEvent{
						EventKey: wantKey, JobID: claim.GetId(), WorkflowID: workflow.GetId(), UserID: workflow.GetUserId(),
						Message: "restored " + stream, TimeStamp: wantTime, Stream: stream, SequenceNum: 1, Retention: retained,
					}
					if !reflect.DeepEqual(event, want) {
						t.Fatalf("durable event = %+v; want %+v", event, want)
					}
				}
			}
		})
	}
}

// Poll one record at a time so a fetch containing both streams cannot discard
// the second record when the first expected record is returned.
func nextReplayRecord(ctx context.Context, t *testing.T, consumer *kgo.Client, jobID string) *kgo.Record {
	t.Helper()
	for ctx.Err() == nil {
		fetches := consumer.PollRecords(ctx, 1)
		if err := fetches.Err(); err != nil {
			t.Fatalf("poll replayed logs: %v", err)
		}
		var matching *kgo.Record
		fetches.EachRecord(func(record *kgo.Record) {
			if record.Topic == kafkapkg.TopicJobLogs && string(record.Key) == jobID {
				matching = record
			}
		})
		if matching != nil {
			return matching
		}
	}
	t.Fatalf("no replayed log for %s: %v", jobID, ctx.Err())
	return nil
}

func TestIntegrationExecutorLiveLogQueueDropsWithoutClaimingPublication(t *testing.T) {
	store := testkit.Redis(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	jobID := uuid.NewString()
	pubsub := store.Subscribe(ctx, redis.GetJobLogsChannel(jobID))
	defer func() {
		if err := pubsub.Close(); err != nil {
			t.Errorf("close subscription: %v", err)
		}
	}()
	if _, err := pubsub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	repo := &Repository{livePublisher: joblogevents.NewLivePublisher(store, joblogevents.LivePublisherConfig{BufferSize: 1})}
	workflowID, userID := uuid.NewString(), uuid.NewString()
	stamp := time.Now().UTC()
	newEvent := func(sequence uint32, message string, retained bool) *jobsmodel.JobLogEvent {
		return &jobsmodel.JobLogEvent{
			EventKey: fmt.Sprintf("log:%s:stdout:%d", jobID, sequence), JobID: jobID,
			WorkflowID: workflowID, UserID: userID, Message: message, TimeStamp: stamp,
			Stream: "stdout", SequenceNum: sequence, Retention: retained,
		}
	}
	first := newEvent(1, "queued", true)
	dropped := newEvent(2, "full queue", true)
	unretained := newEvent(3, "durable only", false)
	// Retention must reject the event while queue capacity is available.
	repo.publishLiveJobLog(ctx, unretained)
	repo.publishLiveJobLog(ctx, first)
	repo.publishLiveJobLog(ctx, dropped)
	if !first.LivePublished || dropped.LivePublished || unretained.LivePublished {
		t.Fatalf("publication flags = %t/%t/%t", first.LivePublished, dropped.LivePublished, unretained.LivePublished)
	}
	want := *first
	first.Message = "caller changed the original"
	workerCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := repo.runLiveJobLogPublisher(workerCtx, zap.NewNop())
	defer func() {
		stop()
		select {
		case <-done:
		case <-ctx.Done():
			t.Error("live publisher did not exit after cancellation")
		}
	}()
	message, err := pubsub.ReceiveMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var delivered jobsmodel.JobLogEvent
	if decodeErr := json.Unmarshal([]byte(message.Payload), &delivered); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if !reflect.DeepEqual(delivered, want) {
		t.Fatalf("delivered live event = %+v; want queued snapshot %+v", delivered, want)
	}
}
