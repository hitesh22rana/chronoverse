//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package analyticsprocessor

import (
	"context"

	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// Service provides analyticsprocessor related operations.
type Service interface {
	Run(ctx context.Context) error
}

// AnalyticsProcessor represents the analyticsprocessor.
type AnalyticsProcessor struct {
	logger *zap.Logger
	svc    Service
}

// New creates a new analyticsprocessor.
func New(ctx context.Context, svc Service) *AnalyticsProcessor {
	return &AnalyticsProcessor{
		logger: loggerpkg.FromContext(ctx),
		svc:    svc,
	}
}

// Run starts the analyticsprocessor.
func (e *AnalyticsProcessor) Run(ctx context.Context) error {
	err := e.svc.Run(ctx)
	if err != nil {
		e.logger.Error("error occurred while running the analytics processor", zap.Error(err))
	} else {
		e.logger.Info("successfully exited the analytics processor")
	}

	return nil
}
