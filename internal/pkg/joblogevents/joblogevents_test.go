package joblogevents_test

import (
	"encoding/json"
	"testing"
	"time"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

func TestKafkaRecord(t *testing.T) {
	t.Parallel()

	event := &jobsmodel.JobLogEvent{
		EventKey:      "log:job-1:stdout:1",
		JobID:         "job-1",
		WorkflowID:    "workflow-1",
		UserID:        "user-1",
		Message:       "hello",
		TimeStamp:     time.Now().UTC(),
		SequenceNum:   1,
		Stream:        "stdout",
		Retention:     true,
		LivePublished: true,
	}

	record, err := joblogevents.KafkaRecord(event)
	if err != nil {
		t.Fatalf("KafkaRecord() error = %v", err)
	}
	if record.Topic != kafka.TopicJobLogs {
		t.Fatalf("KafkaRecord() topic = %q, want %q", record.Topic, kafka.TopicJobLogs)
	}
	if string(record.Key) != event.JobID {
		t.Fatalf("KafkaRecord() key = %q, want %q", string(record.Key), event.JobID)
	}

	var got jobsmodel.JobLogEvent
	if err := json.Unmarshal(record.Value, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.EventKey != event.EventKey || got.JobID != event.JobID || !got.LivePublished {
		t.Fatalf("KafkaRecord() payload = %+v, want event key %q, job %q, live published true", got, event.EventKey, event.JobID)
	}
}

func TestPublishLiveSkipsNilRedisAndEphemeralLogs(t *testing.T) {
	t.Parallel()

	published, err := joblogevents.PublishLive(t.Context(), nil, &jobsmodel.JobLogEvent{
		JobID:     "job-1",
		Retention: true,
	})
	if err != nil {
		t.Fatalf("PublishLive() nil redis error = %v", err)
	}
	if published {
		t.Fatal("PublishLive() nil redis published = true, want false")
	}

	published, err = joblogevents.PublishLive(t.Context(), nil, &jobsmodel.JobLogEvent{
		JobID:     "job-1",
		Retention: false,
	})
	if err != nil {
		t.Fatalf("PublishLive() ephemeral error = %v", err)
	}
	if published {
		t.Fatal("PublishLive() ephemeral published = true, want false")
	}
}
