//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package joblogs

import (
	"context"

	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// Service provides joblogs related operations.
type Service interface {
	Run(ctx context.Context) error
}

// Joblogs represents the joblogs.
type Joblogs struct {
	logger *zap.Logger
	svc    Service
}

// New creates a new joblogs.
func New(ctx context.Context, svc Service) *Joblogs {
	return &Joblogs{
		logger: loggerpkg.FromContext(ctx),
		svc:    svc,
	}
}

// Run starts the joblogs.
func (e *Joblogs) Run(ctx context.Context) error {
	err := e.svc.Run(ctx)
	if err != nil {
		e.logger.Error("error occurred while running the joblogs job", zap.Error(err))
	} else {
		e.logger.Info("successfully exicted the joblogs job")
	}

	return nil
}
