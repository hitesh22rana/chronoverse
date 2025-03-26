//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package joblogs

import (
	"context"
)

// Repository provides joblogs related operations.
type Repository interface {
	Run(ctx context.Context) error
}

// Service provides joblogs related operations.
type Service struct {
	repo Repository
}

// New creates a new joblogs service.
func New(repo Repository) *Service {
	return &Service{
		repo: repo,
	}
}

// Run starts the joblogs.
func (s *Service) Run(ctx context.Context) (err error) {
	err = s.repo.Run(ctx)
	if err != nil {
		return err
	}

	return nil
}
