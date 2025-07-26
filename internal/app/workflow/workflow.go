//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package workflow

import (
	"context"

	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// Service provides workflow related operations.
type Service interface {
	Run(ctx context.Context) error
}

// Workflow represents the workflow.
type Workflow struct {
	logger *zap.Logger
	svc    Service
}

// New creates a new workflow.
func New(ctx context.Context, svc Service) *Workflow {
	return &Workflow{
		logger: loggerpkg.FromContext(ctx),
		svc:    svc,
	}
}

// Run starts the workflow.
func (e *Workflow) Run(ctx context.Context) error {
	err := e.svc.Run(ctx)
	if err != nil {
		e.logger.Error("error occurred while running the workflow job", zap.Error(err))
	} else {
		e.logger.Info("successfully exited the workflow job")
	}

	return nil
}
