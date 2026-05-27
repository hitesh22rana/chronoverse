//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package analyticsprocessor

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// Service provides analyticsprocessor related operations.
type Service interface {
	Run(ctx context.Context) error
	CleanupProcessedEvents(ctx context.Context, retention time.Duration, batchSize int) (int64, error)
}

// Config controls analytics processor background maintenance.
type Config struct {
	CleanupEnabled           bool
	CleanupInterval          time.Duration
	CleanupBatchSize         int
	ProcessedEventsRetention time.Duration
}

// AnalyticsProcessor represents the analyticsprocessor.
type AnalyticsProcessor struct {
	logger *zap.Logger
	cfg    *Config
	svc    Service
}

// New creates a new analyticsprocessor.
func New(ctx context.Context, cfg *Config, svc Service) *AnalyticsProcessor {
	if cfg == nil {
		cfg = &Config{}
	}
	return &AnalyticsProcessor{
		logger: loggerpkg.FromContext(ctx),
		cfg:    cfg,
		svc:    svc,
	}
}

// Run starts the analyticsprocessor.
func (e *AnalyticsProcessor) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	if e.cfg.CleanupEnabled && e.cfg.CleanupInterval > 0 && e.cfg.CleanupBatchSize > 0 {
		wg.Go(func() {
			e.runCleanup(ctx)
		})
	}

	err := e.svc.Run(ctx)
	cancel()
	wg.Wait()
	if err != nil {
		e.logger.Error("error occurred while running the analytics processor", zap.Error(err))
	} else {
		e.logger.Info("successfully exited the analytics processor")
	}

	return nil
}

func (e *AnalyticsProcessor) runCleanup(ctx context.Context) {
	ticker := time.NewTicker(e.cfg.CleanupInterval)
	defer ticker.Stop()

	e.cleanupOnce(ctx)
	for {
		select {
		case <-ticker.C:
			e.cleanupOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (e *AnalyticsProcessor) cleanupOnce(ctx context.Context) {
	total, err := e.svc.CleanupProcessedEvents(ctx, e.cfg.ProcessedEventsRetention, e.cfg.CleanupBatchSize)
	if err != nil {
		e.logger.Error("failed to cleanup processed events", zap.Error(err))
		return
	}
	if total > 0 {
		e.logger.Info("cleaned up processed events", zap.Int64("total", total))
	}
}
