//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package scheduler

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Service provides scheduler related operations.
type Service interface {
	Run(ctx context.Context) (int, error)
}

// Config represents the jobs-service configuration.
type Config struct {
	PollInterval time.Duration
}

// Scheduler represents the scheduler.
type Scheduler struct {
	logger *zap.Logger
	tp     trace.Tracer
	cfg    *Config
	svc    Service
}

// New creates a new scheduler.
func New(ctx context.Context, cfg *Config, svc Service) *Scheduler {
	return &Scheduler{
		logger: loggerpkg.FromContext(ctx),
		tp:     otel.Tracer(svcpkg.Info().GetName()),
		cfg:    cfg,
		svc:    svc,
	}
}

// Run starts the scheduler.
func (s *Scheduler) Run(ctx context.Context) error {
	ctx, span := s.tp.Start(ctx, "App.Run", trace.WithAttributes(
		attribute.String("poll_interval", s.cfg.PollInterval.String()),
	))
	defer span.End()

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			total, err := s.svc.Run(ctx)
			if err != nil {
				span.SetStatus(otelcodes.Error, err.Error())
				span.RecordError(err)
				s.logger.Error("failed to run the scheduler", zap.Error(err))
			}

			s.logger.Info("successfully scheduled jobs", zap.Int("total", total))
		case <-ctx.Done():
			s.logger.Info("stopping the scheduler")
			return nil
		}
	}
}
