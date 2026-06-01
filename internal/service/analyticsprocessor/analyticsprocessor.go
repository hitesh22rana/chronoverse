//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package analyticsprocessor

import (
	"context"
	"time"
)

// Repository provides analyticsprocessor related operations.
type Repository interface {
	Run(ctx context.Context) error
	CleanupProcessedEvents(ctx context.Context, retention time.Duration, batchSize int) (int64, error)
}

// Service provides analyticsprocessor related operations.
type Service struct {
	repo Repository
}

// New creates a new analyticsprocessor service.
func New(repo Repository) *Service {
	return &Service{
		repo: repo,
	}
}

// Run starts the analyticsprocessor.
func (s *Service) Run(ctx context.Context) (err error) {
	err = s.repo.Run(ctx)
	if err != nil {
		return err
	}

	return nil
}

// CleanupProcessedEvents deletes old processed-event dedupe rows.
func (s *Service) CleanupProcessedEvents(ctx context.Context, retention time.Duration, batchSize int) (int64, error) {
	return s.repo.CleanupProcessedEvents(ctx, retention, batchSize)
}
