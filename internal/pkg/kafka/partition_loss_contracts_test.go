package kafka_test

import (
	"context"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

func TestPartitionLossCancelsOnlyLostLane(t *testing.T) {
	t.Parallel()
	client := newFakePartitionClient()
	lifecycle := kafka.NewPartitionLifecycle()
	started, canceled := make(chan struct{}), make(chan struct{})
	runner := kafka.NewPartitionRunner(client, func(ctx context.Context, rec *kgo.Record) error {
		if rec.Partition != 0 {
			return nil
		}
		close(started)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	}, &kafka.PartitionRunnerConfig{Name: "partition-loss", RetryBackoff: time.Millisecond, MaxBackoff: time.Millisecond}, lifecycle)
	ctx, cancel := context.WithCancel(t.Context())
	done := runPartitionRunner(ctx, t, runner)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("runner did not shut down")
		}
	})
	lifecycle.OnAssigned(ctx, nil, map[string][]int32{"topic": {0, 1}})
	assertPartitionSignal(t, client.resumed, testPartitionKey{topic: "topic", partition: 0})
	assertPartitionSignal(t, client.resumed, testPartitionKey{topic: "topic", partition: 1})
	client.fetches <- fetches(0)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("lost lane did not start")
	}
	lifecycle.OnLost(ctx, nil, map[string][]int32{"topic": {0}})
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("partition loss did not cancel in-flight handler")
	}
	assertPartitionSignal(t, client.resumed, testPartitionKey{topic: "topic", partition: 0})
	client.fetches <- kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "topic", Partitions: []kgo.FetchPartition{
		{Partition: 0, Records: []*kgo.Record{record(0, 1)}},
		{Partition: 1, Records: []*kgo.Record{record(1, 1)}},
	}}}}}
	assertCommitRecord(t, client.commits, 1, 1)
	assertNoCommit(t, client.commits, 50*time.Millisecond)
	assertPartitionLossShutdownHasNoCommits(t, cancel, done, client.commits)
}

func TestPartitionLossBeforeRunnerRegistrationPreservesRetainedAssignment(t *testing.T) {
	t.Parallel()
	client := newFakePartitionClient()
	lifecycle := kafka.NewPartitionLifecycle()
	ctx, cancel := context.WithCancel(t.Context())
	lifecycle.OnAssigned(ctx, nil, map[string][]int32{"topic": {0, 1}})
	lifecycle.OnLost(ctx, nil, map[string][]int32{"topic": {0}})
	runner := kafka.NewPartitionRunner(client, func(context.Context, *kgo.Record) error { return nil }, &kafka.PartitionRunnerConfig{Name: "register-after-loss"}, lifecycle)
	assertPartitionSignal(t, client.resumed, testPartitionKey{topic: "topic", partition: 1})
	done := runPartitionRunner(ctx, t, runner)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("runner did not shut down")
		}
	})
	client.fetches <- kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "topic", Partitions: []kgo.FetchPartition{
		{Partition: 0, Records: []*kgo.Record{record(0, 0)}},
		{Partition: 1, Records: []*kgo.Record{record(1, 0)}},
	}}}}}
	assertCommitRecord(t, client.commits, 1, 0)
	assertNoCommit(t, client.commits, 50*time.Millisecond)
	assertPartitionLossShutdownHasNoCommits(t, cancel, done, client.commits)
}

func assertPartitionLossShutdownHasNoCommits(t *testing.T, cancel context.CancelFunc, done <-chan struct{}, commits <-chan *kgo.Record) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not shut down")
	}
	select {
	case commit := <-commits:
		t.Fatalf("late commit after partition loss: partition %d offset %d", commit.Partition, commit.Offset)
	default:
	}
}

func TestNilPartitionLifecycleCallbacksAreNoOps(t *testing.T) {
	t.Parallel()
	var lifecycle *kafka.PartitionLifecycle
	partitions := map[string][]int32{"topic": {0}}
	lifecycle.OnAssigned(t.Context(), nil, partitions)
	lifecycle.OnRevoked(t.Context(), nil, partitions)
	lifecycle.OnLost(t.Context(), nil, partitions)
	if len(partitions["topic"]) != 1 || partitions["topic"][0] != 0 {
		t.Fatal("optional lifecycle callbacks mutated assignments")
	}
}
