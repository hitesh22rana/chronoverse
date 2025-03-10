//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package executor

import (
	"context"
)

// Repository provides executor related operations.
type Repository interface {
	Run(ctx context.Context) error
}

// Service provides executor related operations.
type Service struct {
	repo Repository
}

// New creates a new executor service.
func New(repo Repository) *Service {
	return &Service{
		repo: repo,
	}
}

// Run starts the executor.
func (s *Service) Run(ctx context.Context) (err error) {
	err = s.repo.Run(ctx)
	if err != nil {
		return err
	}

	return nil
}
