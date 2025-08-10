//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package executor

import (
	"context"

	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// Service provides executor related operations.
type Service interface {
	Run(ctx context.Context) error
}

// Executor represents the executor.
type Executor struct {
	logger *zap.Logger
	svc    Service
}

// New creates a new executor.
func New(ctx context.Context, svc Service) *Executor {
	return &Executor{
		logger: loggerpkg.FromContext(ctx),
		svc:    svc,
	}
}

// Run starts the executor.
func (e *Executor) Run(ctx context.Context) error {
	err := e.svc.Run(ctx)
	if err != nil {
		e.logger.Error("error occurred while running the executor job", zap.Error(err))
	} else {
		e.logger.Info("successfully exited the executor job")
	}

	return nil
}
