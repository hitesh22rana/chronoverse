//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package analyticsprocessor

import (
	"context"
)

// Repository provides analyticsprocessor related operations.
type Repository interface {
	Run(ctx context.Context) error
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
