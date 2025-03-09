//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package scheduler

import (
	"context"
	"time"

	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// Service provides scheduler related operations.
type Service interface {
	Run(ctx context.Context) (int, error)
}

// Config represents the jobs-service configuration.
type Config struct {
	PollInterval   time.Duration
	ContextTimeout time.Duration
}

// Scheduler represents the scheduler.
type Scheduler struct {
	logger *zap.Logger
	cfg    *Config
	svc    Service
}

// New creates a new scheduler.
func New(ctx context.Context, cfg *Config, svc Service) *Scheduler {
	return &Scheduler{
		logger: loggerpkg.FromContext(ctx),
		cfg:    cfg,
		svc:    svc,
	}
}

// Run starts the scheduler.
func (s *Scheduler) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctxTimeout, cancel := context.WithTimeout(ctx, s.cfg.ContextTimeout)

			total, err := s.svc.Run(ctxTimeout)
			// The context is canceled, so we don't need to call cancel.
			cancel()

			if err != nil {
				s.logger.Error("failed to run the scheduler", zap.Error(err))
			} else {
				s.logger.Info("successfully scheduled jobs", zap.Int("total", total))
			}
		case <-ctx.Done():
			s.logger.Info("stopping the scheduler")
			return nil
		}
	}
}
