package joblogevents

import (
	"context"
	"encoding/json"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

// KafkaRecord returns a Kafka record for a job log event.
func KafkaRecord(event *jobsmodel.JobLogEvent) (*kgo.Record, error) {
	payload, err := Marshal(event)
	if err != nil {
		return nil, err
	}

	return &kgo.Record{
		Topic: kafka.TopicJobLogs,
		Key:   []byte(event.JobID),
		Value: payload,
	}, nil
}

// Marshal returns the JSON payload for a job log event.
func Marshal(event *jobsmodel.JobLogEvent) ([]byte, error) {
	if event == nil {
		return nil, status.Error(codes.InvalidArgument, "job log event is required")
	}
	if event.EventKey == "" {
		event.EventKey = idempotency.LogEventKey(event.JobID, event.Stream, event.SequenceNum)
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal job log event: %v", err)
	}

	return payload, nil
}

// PublishLive publishes a retained job log event to the live Redis stream.
func PublishLive(ctx context.Context, rdb *redis.Store, event *jobsmodel.JobLogEvent) (bool, error) {
	if event == nil {
		return false, status.Error(codes.InvalidArgument, "job log event is required")
	}
	if rdb == nil || !event.Retention {
		return false, nil
	}

	payload, err := Marshal(event)
	if err != nil {
		return false, err
	}
	if err := rdb.Publish(ctx, redis.GetJobLogsChannel(event.JobID), payload); err != nil {
		return false, err
	}

	return true, nil
}
