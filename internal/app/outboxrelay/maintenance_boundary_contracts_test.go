//nolint:testpackage // Checks private cleanup scheduling with virtual time.
package outboxrelay

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type maintenanceService struct {
	outbox, ledger              int64
	outboxErr, ledgerErr        error
	publishedCalls, ledgerCalls int
	ledgerObserved              chan struct{}
}

func (*maintenanceService) PublishTopic(context.Context, string, *zap.Logger) (int, error) {
	panic("unexpected publish during maintenance")
}

func (s *maintenanceService) CleanupPublishedEvents(_ context.Context, _ time.Duration, _ int) (int64, error) {
	s.publishedCalls++
	return s.outbox, s.outboxErr
}

func (s *maintenanceService) CleanupCommandIdempotencyKeys(context.Context, int) (int64, error) {
	s.ledgerCalls++
	if s.ledgerObserved != nil {
		s.ledgerObserved <- struct{}{}
	}
	return s.ledger, s.ledgerErr
}

func TestCleanupOutcomesKeepBothStoresIndependent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		svc      maintenanceService
		messages []string
	}{
		{"empty", maintenanceService{}, nil},
		{"deleted", maintenanceService{outbox: 2, ledger: 3}, []string{"cleaned up published outbox events", "cleaned up command idempotency keys"}},
		{"backlog", maintenanceService{ledger: 10}, []string{"cleaned up command idempotency keys", "command idempotency cleanup reached its batch limit; backlog may remain"}},
		{
			"both fail",
			maintenanceService{outboxErr: errors.New("outbox unavailable"), ledgerErr: errors.New("ledger unavailable")},
			[]string{"failed to cleanup published outbox events", "failed to cleanup command idempotency keys"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			app := &OutboxRelay{logger: zap.New(core), svc: &tc.svc, cfg: &Config{ContextTimeout: time.Second, CleanupBatchSize: 10, IdempotencyCleanupMaxBatches: 1}}
			app.cleanupOnce(t.Context())
			require.Equal(t, 1, tc.svc.publishedCalls)
			require.Equal(t, 1, tc.svc.ledgerCalls)
			require.Len(t, logs.All(), len(tc.messages))
			for index, want := range tc.messages {
				require.Equal(t, want, logs.All()[index].Message)
			}
		})
	}
}

func TestCleanupRunsImmediatelyThenOnTickAndStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &maintenanceService{ledgerObserved: make(chan struct{}, 2)}
		app := &OutboxRelay{logger: zap.NewNop(), svc: svc, cfg: &Config{CleanupInterval: time.Second, ContextTimeout: time.Second, CleanupBatchSize: 10, IdempotencyCleanupMaxBatches: 1}}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		start := time.Now()
		go func() { defer close(done); app.runCleanup(ctx) }()
		<-svc.ledgerObserved
		require.Zero(t, time.Since(start))
		time.Sleep(time.Second)
		<-svc.ledgerObserved
		require.Equal(t, time.Second, time.Since(start))
		cancel()
		<-done
		require.Equal(t, 2, svc.publishedCalls)
		require.Equal(t, 2, svc.ledgerCalls)
	})
}
