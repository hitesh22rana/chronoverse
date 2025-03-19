//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package workflow

import (
	"context"
)

// Repository provides workflow related operations.
type Repository interface {
	Run(ctx context.Context) error
}

// Service provides workflow related operations.
type Service struct {
	repo Repository
}

// New creates a new workflow service.
func New(repo Repository) *Service {
	return &Service{
		repo: repo,
	}
}

// Run starts the workflow.
func (s *Service) Run(ctx context.Context) (err error) {
	err = s.repo.Run(ctx)
	if err != nil {
		return err
	}

	return nil
}
