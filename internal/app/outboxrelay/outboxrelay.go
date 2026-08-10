package outboxrelay

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// Service publishes claimed outbox events for a Kafka topic.
type Service interface {
	PublishTopic(ctx context.Context, topic string, logger *zap.Logger) (int, error)
	CleanupPublishedEvents(ctx context.Context, retention time.Duration, batchSize int) (int64, error)
	CleanupCommandIdempotencyKeys(ctx context.Context, batchSize int) (int64, error)
}

// Config controls the outbox relay topic handlers.
type Config struct {
	WorkflowEnabled    bool
	JobsEnabled        bool
	AnalyticsEnabled   bool
	PollInterval       time.Duration
	ContextTimeout     time.Duration
	CleanupEnabled     bool
	CleanupInterval    time.Duration
	CleanupBatchSize   int
	PublishedRetention time.Duration
}

// OutboxRelay runs independent outbox publishing loops per enabled topic.
type OutboxRelay struct {
	logger *zap.Logger
	cfg    *Config
	svc    Service
}

// New creates an outbox relay application.
func New(ctx context.Context, cfg *Config, svc Service) *OutboxRelay {
	return &OutboxRelay{
		logger: loggerpkg.FromContext(ctx),
		cfg:    cfg,
		svc:    svc,
	}
}

// Run starts the enabled topic handlers until the context is canceled.
func (o *OutboxRelay) Run(ctx context.Context) error {
	topics := make([]string, 0, 4)
	if o.cfg.WorkflowEnabled {
		topics = append(topics, kafka.TopicWorkflows)
	}
	if o.cfg.JobsEnabled {
		topics = append(topics, kafka.TopicJobs)
	}
	if o.cfg.AnalyticsEnabled {
		topics = append(topics, kafka.TopicAnalytics)
	}

	var wg sync.WaitGroup
	for _, topic := range topics {
		wg.Go(func() {
			o.runTopic(ctx, topic)
		})
	}
	if o.cfg.CleanupEnabled && o.cfg.CleanupInterval > 0 && o.cfg.CleanupBatchSize > 0 {
		wg.Go(func() {
			o.runCleanup(ctx)
		})
	}

	<-ctx.Done()
	wg.Wait()
	return nil
}

func (o *OutboxRelay) runCleanup(ctx context.Context) {
	ticker := time.NewTicker(o.cfg.CleanupInterval)
	defer ticker.Stop()

	o.cleanupOnce(ctx)
	for {
		select {
		case <-ticker.C:
			o.cleanupOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (o *OutboxRelay) cleanupOnce(ctx context.Context) {
	outboxCtx, cancelOutbox := context.WithTimeout(ctx, o.cfg.ContextTimeout)
	total, err := o.svc.CleanupPublishedEvents(outboxCtx, o.cfg.PublishedRetention, o.cfg.CleanupBatchSize)
	cancelOutbox()
	if err != nil {
		o.logger.Error("failed to cleanup published outbox events", zap.Error(err))
	} else if total > 0 {
		o.logger.Info("cleaned up published outbox events", zap.Int64("total", total))
	}

	ledgerCtx, cancelLedger := context.WithTimeout(ctx, o.cfg.ContextTimeout)
	ledgerTotal, ledgerErr := o.svc.CleanupCommandIdempotencyKeys(ledgerCtx, o.cfg.CleanupBatchSize)
	cancelLedger()
	if ledgerErr != nil {
		o.logger.Error("failed to cleanup command idempotency keys", zap.Error(ledgerErr))
	} else if ledgerTotal > 0 {
		o.logger.Info("cleaned up command idempotency keys", zap.Int64("total", ledgerTotal))
	}
}

func (o *OutboxRelay) runTopic(ctx context.Context, topic string) {
	ticker := time.NewTicker(o.cfg.PollInterval)
	defer ticker.Stop()

	logger := o.logger.With(zap.String("topic", topic))
	for {
		select {
		case <-ticker.C:
			ctxTimeout, cancel := context.WithTimeout(ctx, o.cfg.ContextTimeout)
			total, err := o.svc.PublishTopic(ctxTimeout, topic, logger)
			cancel()
			if err != nil {
				logger.Error("failed to publish outbox events", zap.Error(err))
				continue
			}
			if total > 0 {
				logger.Info("published outbox events", zap.Int("total", total))
			}
		case <-ctx.Done():
			return
		}
	}
}
