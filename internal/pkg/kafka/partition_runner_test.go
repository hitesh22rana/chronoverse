package kafka_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

type fakePartitionClient struct {
	fetches  chan kgo.Fetches
	commits  chan *kgo.Record
	paused   chan testPartitionKey
	resumed  chan testPartitionKey
	commitFn func(*kgo.Record) error
}

type testPartitionKey struct {
	topic     string
	partition int32
}

func newFakePartitionClient() *fakePartitionClient {
	return &fakePartitionClient{
		fetches: make(chan kgo.Fetches, 4),
		commits: make(chan *kgo.Record, 16),
		paused:  make(chan testPartitionKey, 16),
		resumed: make(chan testPartitionKey, 16),
	}
}

func (f *fakePartitionClient) PollFetches(ctx context.Context) kgo.Fetches {
	select {
	case fetches := <-f.fetches:
		return fetches
	case <-ctx.Done():
		return nil
	}
}

func (f *fakePartitionClient) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	for _, record := range records {
		if f.commitFn != nil {
			if err := f.commitFn(record); err != nil {
				return err
			}
		}
		f.commits <- record
	}
	return nil
}

func (f *fakePartitionClient) PauseFetchPartitions(partitions map[string][]int32) map[string][]int32 {
	for topic, topicPartitions := range partitions {
		for _, partition := range topicPartitions {
			f.paused <- testPartitionKey{topic: topic, partition: partition}
		}
	}
	return partitions
}

func (f *fakePartitionClient) ResumeFetchPartitions(partitions map[string][]int32) {
	for topic, topicPartitions := range partitions {
		for _, partition := range topicPartitions {
			f.resumed <- testPartitionKey{topic: topic, partition: partition}
		}
	}
}

func TestPartitionRunnerDoesNotCommitPastRetryableRecord(t *testing.T) {
	t.Parallel()

	client := newFakePartitionClient()
	secondAttempt := make(chan struct{})
	allowRetry := make(chan struct{})
	var attempts int32
	var once sync.Once

	runner := kafka.NewPartitionRunner(client, func(ctx context.Context, record *kgo.Record) error {
		if record.Offset != 0 {
			return nil
		}

		if atomic.AddInt32(&attempts, 1) == 1 {
			return status.Error(codes.Unavailable, "transient")
		}

		once.Do(func() { close(secondAttempt) })
		select {
		case <-allowRetry:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, &kafka.PartitionRunnerConfig{
		Name:         "test",
		RetryBackoff: time.Millisecond,
		MaxBackoff:   time.Millisecond,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runPartitionRunner(ctx, t, runner)

	client.fetches <- fetches(0, 1)

	select {
	case <-secondAttempt:
	case <-time.After(time.Second):
		t.Fatal("runner did not retry first record")
	}

	select {
	case commit := <-client.commits:
		t.Fatalf("unexpected commit before retry completed: offset %d", commit.Offset)
	case <-time.After(20 * time.Millisecond):
	}

	close(allowRetry)
	assertCommit(t, client.commits, 0)
	assertCommit(t, client.commits, 1)

	cancel()
	<-done
}

func TestPartitionRunnerContinuesOtherPartitionsDuringRetry(t *testing.T) {
	t.Parallel()

	client := newFakePartitionClient()
	p0RetryStarted := make(chan struct{})
	allowP0 := make(chan struct{})
	var attempts int32
	var once sync.Once

	runner := kafka.NewPartitionRunner(client, func(ctx context.Context, record *kgo.Record) error {
		if record.Partition == 0 {
			if atomic.AddInt32(&attempts, 1) == 1 {
				return status.Error(codes.Unavailable, "transient")
			}
			once.Do(func() { close(p0RetryStarted) })
			select {
			case <-allowP0:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}, &kafka.PartitionRunnerConfig{
		Name:         "test",
		RetryBackoff: time.Millisecond,
		MaxBackoff:   time.Millisecond,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runPartitionRunner(ctx, t, runner)

	client.fetches <- kgo.Fetches{{
		Topics: []kgo.FetchTopic{{
			Topic: "topic",
			Partitions: []kgo.FetchPartition{
				{Partition: 0, Records: []*kgo.Record{record(0, 0)}},
				{Partition: 1, Records: []*kgo.Record{record(1, 0)}},
			},
		}},
	}}

	select {
	case <-p0RetryStarted:
	case <-time.After(time.Second):
		t.Fatal("partition 0 did not start processing")
	}

	assertCommit(t, client.commits, 0)

	close(allowP0)
	assertCommit(t, client.commits, 0)

	cancel()
	<-done
}

func TestPartitionRunnerCommitsNonRetryableErrors(t *testing.T) {
	t.Parallel()

	client := newFakePartitionClient()
	runner := kafka.NewPartitionRunner(client, func(context.Context, *kgo.Record) error {
		return status.Error(codes.InvalidArgument, "bad record")
	}, &kafka.PartitionRunnerConfig{
		Name:         "test",
		RetryBackoff: time.Millisecond,
		MaxBackoff:   time.Millisecond,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runPartitionRunner(ctx, t, runner)

	client.fetches <- fetches(0)
	assertCommit(t, client.commits, 0)

	cancel()
	<-done
}

func TestPartitionRunnerRetriesCommitWithoutReprocessing(t *testing.T) {
	t.Parallel()

	client := newFakePartitionClient()
	firstCommitFailed := make(chan struct{})
	allowCommitRetry := make(chan struct{})
	var processed int32
	var commitAttempts int32
	var once sync.Once

	client.commitFn = func(record *kgo.Record) error {
		attempt := atomic.AddInt32(&commitAttempts, 1)
		if attempt == 1 {
			once.Do(func() { close(firstCommitFailed) })
			return errors.New("commit failed")
		}
		if record.Offset == 0 {
			select {
			case <-allowCommitRetry:
			case <-time.After(time.Second):
				return errors.New("commit retry not released")
			}
		}
		return nil
	}

	runner := kafka.NewPartitionRunner(client, func(context.Context, *kgo.Record) error {
		atomic.AddInt32(&processed, 1)
		return nil
	}, &kafka.PartitionRunnerConfig{
		Name:         "test",
		RetryBackoff: time.Millisecond,
		MaxBackoff:   time.Millisecond,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runPartitionRunner(ctx, t, runner)

	client.fetches <- fetches(0, 1)

	select {
	case <-firstCommitFailed:
	case <-time.After(time.Second):
		t.Fatal("runner did not attempt first commit")
	}

	assertPartitionSignal(t, client.paused, testPartitionKey{topic: "topic", partition: 0})
	if got := atomic.LoadInt32(&processed); got != 1 {
		t.Fatalf("processed %d records before first offset committed, want 1", got)
	}
	select {
	case commit := <-client.commits:
		t.Fatalf("unexpected commit before retry completed: offset %d", commit.Offset)
	case <-time.After(20 * time.Millisecond):
	}

	close(allowCommitRetry)
	assertCommit(t, client.commits, 0)
	assertPartitionSignal(t, client.resumed, testPartitionKey{topic: "topic", partition: 0})
	assertCommit(t, client.commits, 1)
	if got := atomic.LoadInt32(&processed); got != 2 {
		t.Fatalf("processed %d records after commits, want 2", got)
	}

	cancel()
	<-done
}

func TestPartitionRunnerResumesPausedPartitionOnRevoke(t *testing.T) {
	t.Parallel()

	client := newFakePartitionClient()
	lifecycle := kafka.NewPartitionLifecycle()
	runner := kafka.NewPartitionRunner(client, func(context.Context, *kgo.Record) error {
		return status.Error(codes.Unavailable, "transient")
	}, &kafka.PartitionRunnerConfig{
		Name:         "test",
		RetryBackoff: time.Hour,
		MaxBackoff:   time.Hour,
	}, lifecycle)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runPartitionRunner(ctx, t, runner)
	partition := testPartitionKey{topic: "topic", partition: 0}

	lifecycle.OnAssigned(ctx, nil, map[string][]int32{"topic": {0}})
	assertPartitionSignal(t, client.resumed, partition)

	client.fetches <- fetches(0)
	assertPartitionSignal(t, client.paused, partition)

	lifecycle.OnRevoked(ctx, nil, map[string][]int32{"topic": {0}})
	assertPartitionSignal(t, client.resumed, partition)

	cancel()
	<-done
}

func TestPartitionRunnerDoesNotCommitFromStaleLaneAfterReassignment(t *testing.T) {
	t.Parallel()

	client := newFakePartitionClient()
	lifecycle := kafka.NewPartitionLifecycle()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var once sync.Once

	runner := kafka.NewPartitionRunner(client, func(_ context.Context, record *kgo.Record) error {
		if record.Offset == 0 {
			once.Do(func() { close(firstStarted) })
			<-releaseFirst
		}
		return nil
	}, &kafka.PartitionRunnerConfig{
		Name:         "test",
		RetryBackoff: time.Millisecond,
		MaxBackoff:   time.Millisecond,
	}, lifecycle)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runPartitionRunner(ctx, t, runner)
	partition := testPartitionKey{topic: "topic", partition: 0}

	lifecycle.OnAssigned(ctx, nil, map[string][]int32{"topic": {0}})
	assertPartitionSignal(t, client.resumed, partition)

	client.fetches <- fetches(0)
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first lane did not start processing")
	}

	lifecycle.OnRevoked(ctx, nil, map[string][]int32{"topic": {0}})
	assertPartitionSignal(t, client.resumed, partition)
	lifecycle.OnAssigned(ctx, nil, map[string][]int32{"topic": {0}})
	assertPartitionSignal(t, client.resumed, partition)

	client.fetches <- fetches(1)
	assertCommit(t, client.commits, 1)

	close(releaseFirst)
	assertNoCommit(t, client.commits, 50*time.Millisecond)

	cancel()
	<-done
}

func TestPartitionRunnerDispatchesOtherPartitionsWhenLaneBacklogged(t *testing.T) {
	t.Parallel()

	client := newFakePartitionClient()
	p0Started := make(chan struct{})
	releaseP0 := make(chan struct{})
	var once sync.Once

	runner := kafka.NewPartitionRunner(client, func(ctx context.Context, record *kgo.Record) error {
		if record.Partition == 0 && record.Offset == 0 {
			once.Do(func() { close(p0Started) })
			select {
			case <-releaseP0:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}, &kafka.PartitionRunnerConfig{
		Name:          "test",
		RetryBackoff:  time.Millisecond,
		MaxBackoff:    time.Millisecond,
		ChannelBuffer: 1,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runPartitionRunner(ctx, t, runner)

	client.fetches <- kgo.Fetches{{
		Topics: []kgo.FetchTopic{{
			Topic: "topic",
			Partitions: []kgo.FetchPartition{
				{Partition: 0, Records: []*kgo.Record{
					record(0, 0),
					record(0, 1),
					record(0, 2),
				}},
				{Partition: 1, Records: []*kgo.Record{record(1, 0)}},
			},
		}},
	}}

	select {
	case <-p0Started:
	case <-time.After(time.Second):
		t.Fatal("partition 0 did not start processing")
	}

	assertCommitRecord(t, client.commits, 1, 0)

	close(releaseP0)
	assertCommitRecord(t, client.commits, 0, 0)
	assertCommitRecord(t, client.commits, 0, 1)
	assertCommitRecord(t, client.commits, 0, 2)

	cancel()
	<-done
}

func TestPartitionBatchRunnerProcessesFetchedPartitionBatch(t *testing.T) {
	t.Parallel()

	client := newFakePartitionClient()
	processed := make(chan []int64, 1)
	runner := kafka.NewPartitionBatchRunner(client, func(_ context.Context, records []*kgo.Record) error {
		offsets := make([]int64, 0, len(records))
		for _, record := range records {
			offsets = append(offsets, record.Offset)
		}
		processed <- offsets
		return nil
	}, &kafka.PartitionRunnerConfig{
		Name:          "test",
		RetryBackoff:  time.Millisecond,
		MaxBackoff:    time.Millisecond,
		BatchSize:     3,
		BatchInterval: time.Hour,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runPartitionRunner(ctx, t, runner)

	client.fetches <- fetches(0, 1, 2)

	select {
	case offsets := <-processed:
		if len(offsets) != 3 || offsets[0] != 0 || offsets[1] != 1 || offsets[2] != 2 {
			t.Fatalf("processed offsets %v, want [0 1 2]", offsets)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for batch processing")
	}
	assertCommit(t, client.commits, 0)
	assertCommit(t, client.commits, 1)
	assertCommit(t, client.commits, 2)

	cancel()
	<-done
}

func TestPartitionRunnerKeepsProcessorSpanUnderRecordSpan(t *testing.T) {
	t.Parallel()

	client := newFakePartitionClient()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Fatalf("failed to shutdown tracer provider: %v", err)
		}
	})
	tracer := provider.Tracer("test")

	runner := kafka.NewPartitionRunner(client, func(ctx context.Context, _ *kgo.Record) error {
		_, span := tracer.Start(ctx, "processor.child")
		span.End()
		return nil
	}, &kafka.PartitionRunnerConfig{
		Name:         "telemetry.runner",
		RetryBackoff: time.Millisecond,
		MaxBackoff:   time.Millisecond,
		Tracer:       tracer,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runPartitionRunner(ctx, t, runner)

	client.fetches <- fetches(42)
	assertCommit(t, client.commits, 42)

	var recordSpan, childSpan sdktrace.ReadOnlySpan
	deadline := time.After(time.Second)
	for recordSpan == nil || childSpan == nil {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for telemetry spans")
		default:
		}

		for _, span := range recorder.Ended() {
			switch span.Name() {
			case "telemetry.runner.record":
				recordSpan = span
			case "processor.child":
				childSpan = span
			}
		}
		time.Sleep(time.Millisecond)
	}

	if childSpan.Parent().SpanID() != recordSpan.SpanContext().SpanID() {
		t.Fatal("processor span is not a child of the partition runner record span")
	}
	assertSpanStringAttribute(t, recordSpan, "runner", "telemetry.runner")
	assertSpanStringAttribute(t, recordSpan, "partition_lane", "topic:0")
	assertSpanIntAttribute(t, recordSpan, "commit_attempt", 1)

	cancel()
	<-done
}

func runPartitionRunner(ctx context.Context, t *testing.T, runner *kafka.PartitionRunner) <-chan struct{} {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		err := runner.Run(ctx)
		if err != nil && ctx.Err() == nil {
			t.Errorf("PartitionRunner.Run() returned error: %v", err)
		}
	}()
	return done
}

func fetches(offsets ...int64) kgo.Fetches {
	records := make([]*kgo.Record, 0, len(offsets))
	for _, offset := range offsets {
		records = append(records, record(0, offset))
	}

	return kgo.Fetches{{
		Topics: []kgo.FetchTopic{{
			Topic: "topic",
			Partitions: []kgo.FetchPartition{{
				Partition: 0,
				Records:   records,
			}},
		}},
	}}
}

func record(partition int32, offset int64) *kgo.Record {
	return &kgo.Record{
		Topic:     "topic",
		Partition: partition,
		Offset:    offset,
	}
}

func assertCommit(t *testing.T, commits <-chan *kgo.Record, offset int64) {
	t.Helper()

	assertCommitRecord(t, commits, -1, offset)
}

func assertCommitRecord(t *testing.T, commits <-chan *kgo.Record, partition int32, offset int64) {
	t.Helper()

	select {
	case commit := <-commits:
		if partition >= 0 && commit.Partition != partition {
			t.Fatalf("committed partition %d, want %d", commit.Partition, partition)
		}
		if commit.Offset != offset {
			t.Fatalf("committed offset %d, want %d", commit.Offset, offset)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for commit offset %d", offset)
	}
}

func assertNoCommit(t *testing.T, commits <-chan *kgo.Record, wait time.Duration) {
	t.Helper()

	select {
	case commit := <-commits:
		t.Fatalf("unexpected commit: partition %d offset %d", commit.Partition, commit.Offset)
	case <-time.After(wait):
	}
}

func assertPartitionSignal(t *testing.T, signals <-chan testPartitionKey, want testPartitionKey) {
	t.Helper()

	select {
	case got := <-signals:
		if got != want {
			t.Fatalf("partition signal %+v, want %+v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for partition signal %+v", want)
	}
}

func assertSpanStringAttribute(t *testing.T, span sdktrace.ReadOnlySpan, key, want string) {
	t.Helper()

	for _, attr := range span.Attributes() {
		if string(attr.Key) == key && attr.Value.AsString() == want {
			return
		}
	}
	t.Fatalf("span %q missing attribute %s=%q", span.Name(), key, want)
}

func assertSpanIntAttribute(t *testing.T, span sdktrace.ReadOnlySpan, key string, want int64) {
	t.Helper()

	for _, attr := range span.Attributes() {
		if string(attr.Key) == key && attr.Value.AsInt64() == want {
			return
		}
	}
	t.Fatalf("span %q missing attribute %s=%d", span.Name(), key, want)
}
