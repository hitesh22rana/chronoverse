package outboxrelay_test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/app/outboxrelay"
)

type fakeOutboxRelayService struct {
	cancel context.CancelFunc
	total  int64
}

func (f *fakeOutboxRelayService) PublishTopic(context.Context, string, *zap.Logger) (int, error) {
	return 0, nil
}

func (f *fakeOutboxRelayService) CleanupPublishedEvents(context.Context, time.Duration, int) (int64, error) {
	f.total++
	f.cancel()
	return f.total, nil
}

func TestRunCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	svc := &fakeOutboxRelayService{cancel: cancel}
	app := outboxrelay.New(ctx, &outboxrelay.Config{
		CleanupEnabled:     true,
		CleanupInterval:    time.Hour,
		CleanupBatchSize:   1000,
		ContextTimeout:     time.Second,
		PublishedRetention: 168 * time.Hour,
	}, svc)

	if err := app.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if svc.total != 1 {
		t.Fatalf("expected one cleanup run, got %d", svc.total)
	}
}
