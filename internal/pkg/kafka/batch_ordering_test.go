package kafka_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

func TestBatchRetryKeepsLaterPartialBatchUncommitted(t *testing.T) {
	t.Parallel()
	client := newFakePartitionClient()
	batches := make(chan []int64, 3)
	allowRetry := make(chan struct{})
	calls := 0
	runner := kafka.NewPartitionBatchRunner(client, func(ctx context.Context, records []*kgo.Record) error {
		calls++
		offsets := make([]int64, len(records))
		for i, record := range records {
			offsets[i] = record.Offset
		}
		batches <- offsets
		if calls == 1 {
			return status.Error(codes.Unavailable, "retry batch")
		}
		if calls == 2 {
			select {
			case <-allowRetry:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}, &kafka.PartitionRunnerConfig{BatchSize: 2, BatchInterval: time.Millisecond, RetryBackoff: time.Millisecond, MaxBackoff: time.Millisecond}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runPartitionRunner(ctx, t, runner)
	client.fetches <- fetches(0, 1)
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case got := <-batches:
			if !reflect.DeepEqual(got, []int64{0, 1}) {
				t.Fatalf("retry batch=%v, want [0 1]", got)
			}
		case <-time.After(time.Second):
			t.Fatal("batch attempt did not start")
		}
	}
	client.fetches <- fetches(2)
	assertNoCommit(t, client.commits, 20*time.Millisecond)
	close(allowRetry)
	assertCommit(t, client.commits, 0)
	assertCommit(t, client.commits, 1)
	select {
	case got := <-batches:
		if !reflect.DeepEqual(got, []int64{2}) {
			t.Fatalf("partial batch=%v, want [2]", got)
		}
	case <-time.After(time.Second):
		t.Fatal("partial batch never flushed")
	}
	assertCommit(t, client.commits, 2)
	cancel()
	<-done
}
