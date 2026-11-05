package kafka

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
)

func TestPartitionAssignmentSetOperations(t *testing.T) {
	dst := map[string][]int32{"a": {1, 2}}
	addPartitions(dst, map[string][]int32{"a": {2, 3, 3}, "b": {4}})
	if !reflect.DeepEqual(dst, map[string][]int32{"a": {1, 2, 3}, "b": {4}}) {
		t.Fatalf("assignment = %v", dst)
	}
	removePartitions(dst, map[string][]int32{"a": {}, "b": {4}, "missing": {9}})
	if !reflect.DeepEqual(dst, map[string][]int32{"a": {1, 2, 3}}) {
		t.Fatalf("retained assignments = %v", dst)
	}
}

func TestRunnerLoggerOptionalReplacement(t *testing.T) {
	runner := NewPartitionRunner(nil, nil, nil, nil)
	original := runner.logger
	runner.SetLogger(nil)
	if runner.logger != original {
		t.Fatal("nil logger replaced configured logger")
	}
	replacement := zap.NewNop()
	runner.SetLogger(replacement)
	if runner.logger != replacement {
		t.Fatal("logger replacement ignored")
	}
}

func TestBackoffAndWaitBoundaries(t *testing.T) {
	for _, tt := range []struct{ current, max, want time.Duration }{
		{0, time.Second, defaultPartitionRunnerBackoff},
		{time.Second, 0, 2 * time.Second},
		{time.Second, 3 * time.Second, 2 * time.Second},
		{time.Second, time.Second, time.Second},
	} {
		if got := nextBackoff(tt.current, tt.max); got != tt.want {
			t.Fatalf("nextBackoff(%s,%s)=%s want %s", tt.current, tt.max, got, tt.want)
		}
	}
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		if err := wait(t.Context(), time.Second); err != nil || time.Since(start) != time.Second {
			t.Fatalf("timed wait = %v, %s", err, time.Since(start))
		}
		if err := wait(t.Context(), 0); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled wait = %v", err)
		}
	})
}

func TestBatchQueueCancellationAndDrainBoundaries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lane := &partitionLane{notify: make(chan struct{}, 1)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if records, err := lane.popBatch(ctx, 3, time.Second); records != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("empty canceled batch = %v, %v", records, err)
		}
		first := &kgo.Record{Offset: 1}
		second := &kgo.Record{Offset: 2}
		lane.enqueue([]*kgo.Record{first, second})
		if got := lane.drain(0); got != nil || lane.queueLen() != 2 {
			t.Fatal("zero drain changed queue")
		}
		records, err := lane.popBatch(t.Context(), 1, time.Hour)
		if err != nil || len(records) != 1 || records[0] != first {
			t.Fatalf("single batch = %v, %v", records, err)
		}
		records, err = lane.popBatch(t.Context(), 3, 0)
		if err != nil || len(records) != 1 || records[0] != second {
			t.Fatalf("immediate sparse batch = %v, %v", records, err)
		}
		lane.enqueue([]*kgo.Record{first})
		if records, err = lane.popBatch(ctx, 3, time.Hour); records != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("partial canceled batch = %v, %v", records, err)
		}
	})
}

type scriptedFetchClient struct {
	batchCommitClient
	poll func(context.Context) kgo.Fetches
}

func (c *scriptedFetchClient) PollFetches(ctx context.Context) kgo.Fetches { return c.poll(ctx) }

func TestRunnerFetchErrorEmptyAndClosedLifecycle(t *testing.T) {
	calls := 0
	client := &scriptedFetchClient{}
	client.poll = func(context.Context) kgo.Fetches {
		calls++
		switch calls {
		case 1:
			return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "jobs", Partitions: []kgo.FetchPartition{{Partition: 0, Err: errors.New("temporary fetch failure")}}}}}}
		case 2:
			return nil
		default:
			return kgo.Fetches{{Topics: []kgo.FetchTopic{{Partitions: []kgo.FetchPartition{{Err: kgo.ErrClientClosed}}}}}}
		}
	}
	runner := NewPartitionRunner(client, func(context.Context, *kgo.Record) error { t.Fatal("empty/error fetch processed"); return nil }, nil, nil)
	if err := runner.Run(t.Context()); err != nil || calls != 3 {
		t.Fatalf("Run = %v polls=%d", err, calls)
	}
}

func TestRunnerCanceledBeforePoll(t *testing.T) {
	client := &scriptedFetchClient{poll: func(context.Context) kgo.Fetches { t.Fatal("canceled runner polled"); return nil }}
	runner := NewPartitionRunner(client, nil, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runner.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
}

func TestRecordCanceledBeforeProcessing(t *testing.T) {
	client := &batchCommitClient{commit: func(context.Context, ...*kgo.Record) error { t.Fatal("canceled record committed"); return nil }}
	runner := NewPartitionRunner(client, func(context.Context, *kgo.Record) error { t.Fatal("canceled record processed"); return nil }, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runner.processAndCommit(ctx, &partitionLane{key: partitionKey{topic: "jobs"}}, &kgo.Record{Topic: "jobs"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("processAndCommit = %v", err)
	}
}

func TestBatchQueueCollectsRecordsArrivingDuringInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lane := &partitionLane{notify: make(chan struct{}, 1)}
		first, second := &kgo.Record{Offset: 1}, &kgo.Record{Offset: 2}
		lane.enqueue([]*kgo.Record{first})
		go func() { time.Sleep(time.Millisecond); lane.enqueue([]*kgo.Record{second}) }()
		records, err := lane.popBatch(t.Context(), 2, time.Second)
		if err != nil || !reflect.DeepEqual(records, []*kgo.Record{first, second}) {
			t.Fatalf("batch collection = %v, %v", records, err)
		}
	})
}
