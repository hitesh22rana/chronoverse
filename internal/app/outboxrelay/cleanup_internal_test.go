package outboxrelay

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

type cleanupService struct {
	results []int64
	errAt   int
	calls   int
}

func (*cleanupService) PublishTopic(context.Context, string, *zap.Logger) (int, error) {
	return 0, nil
}

func (*cleanupService) CleanupPublishedEvents(context.Context, time.Duration, int) (int64, error) {
	return 0, nil
}

func (s *cleanupService) CleanupCommandIdempotencyKeys(context.Context, int) (int64, error) {
	s.calls++
	if s.errAt == s.calls {
		return 0, errors.New("cleanup failed")
	}
	if s.calls > len(s.results) {
		return 0, nil
	}
	return s.results[s.calls-1], nil
}

func TestCleanupCommandIdempotencyKeysDrainsPartialBatch(t *testing.T) {
	t.Parallel()

	svc := &cleanupService{results: []int64{1000, 1000, 25}}
	app := &OutboxRelay{cfg: &Config{
		CleanupBatchSize:             1000,
		IdempotencyCleanupMaxBatches: 10,
	}, svc: svc}

	total, batches, drained, err := app.cleanupCommandIdempotencyKeys(t.Context())
	if err != nil {
		t.Fatalf("cleanupCommandIdempotencyKeys() error = %v", err)
	}
	if total != 2025 || batches != 3 || !drained {
		t.Fatalf("cleanup result = total %d, batches %d, drained %t", total, batches, drained)
	}
}

func TestCleanupCommandIdempotencyKeysStopsAtLimit(t *testing.T) {
	t.Parallel()

	svc := &cleanupService{results: []int64{1000, 1000, 1000}}
	app := &OutboxRelay{cfg: &Config{
		CleanupBatchSize:             1000,
		IdempotencyCleanupMaxBatches: 3,
	}, svc: svc}

	total, batches, drained, err := app.cleanupCommandIdempotencyKeys(t.Context())
	if err != nil {
		t.Fatalf("cleanupCommandIdempotencyKeys() error = %v", err)
	}
	if total != 3000 || batches != 3 || drained {
		t.Fatalf("cleanup result = total %d, batches %d, drained %t", total, batches, drained)
	}
}

func TestCleanupCommandIdempotencyKeysStopsOnError(t *testing.T) {
	t.Parallel()

	svc := &cleanupService{results: []int64{1000}, errAt: 2}
	app := &OutboxRelay{cfg: &Config{
		CleanupBatchSize:             1000,
		IdempotencyCleanupMaxBatches: 10,
	}, svc: svc}

	total, batches, drained, err := app.cleanupCommandIdempotencyKeys(t.Context())
	if err == nil || total != 1000 || batches != 1 || drained {
		t.Fatalf("cleanup error result = total %d, batches %d, drained %t, err %v", total, batches, drained, err)
	}
}

func TestCleanupCommandIdempotencyKeysHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	svc := &cleanupService{}
	app := &OutboxRelay{cfg: &Config{
		CleanupBatchSize:             1000,
		IdempotencyCleanupMaxBatches: 10,
	}, svc: svc}

	_, batches, drained, err := app.cleanupCommandIdempotencyKeys(ctx)
	if !errors.Is(err, context.Canceled) || batches != 0 || drained || svc.calls != 0 {
		t.Fatalf("canceled cleanup = batches %d, drained %t, calls %d, err %v", batches, drained, svc.calls, err)
	}
}
