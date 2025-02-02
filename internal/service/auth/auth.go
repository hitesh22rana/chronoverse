//go:generate go run go.uber.org/mock/mockgen@v0.5.0 -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package auth

import (
	"context"
	"fmt"

	"github.com/go-playground/validator/v10"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Repository provides user authentication operations.
type Repository interface {
	Login(ctx context.Context, email, password string) (string, error)
	Register(ctx context.Context, email, password string) (string, error)
	Logout(ctx context.Context, token string) error
	ValidateToken(ctx context.Context, token string) error
}

// Service provides user authentication operations.
type Service struct {
	validator *validator.Validate
	repo      Repository
}

// New creates a new auth service.
func New(validator *validator.Validate, repo Repository) *Service {
	return &Service{
		validator: validator,
		repo:      repo,
	}
}

// RegisterRequest holds the request parameters for registering a new user.
type RegisterRequest struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required,min=8"`
}

// Register a new user.
func (s *Service) Register(ctx context.Context, email, password string) (string, error) {
	// Validate the request
	if err := s.validator.Struct(&RegisterRequest{
		Email:    email,
		Password: password,
	}); err != nil {
		return "", status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}

	pat, err := s.repo.Register(ctx, email, password)
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}

	return pat, nil
}

// LoginRequest holds the request parameters for logging in a user.
type LoginRequest struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required,min=8"`
}

// Login user.
func (s *Service) Login(ctx context.Context, email, password string) (string, error) {
	// Validate the request
	if err := s.validator.Struct(&LoginRequest{
		Email:    email,
		Password: password,
	}); err != nil {
		return "", status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}

	pat, err := s.repo.Login(ctx, email, password)
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}

	return pat, nil
}

// LogoutRequest holds the request parameters for logging out a user.
type LogoutRequest struct {
	Token string `validate:"required"`
}

// Logout user.
func (s *Service) Logout(ctx context.Context, token string) error {
	// Validate the request
	if err := s.validator.Struct(&LogoutRequest{
		Token: token,
	}); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}

	if err := s.repo.Logout(ctx, token); err != nil {
		return fmt.Errorf("%w", err)
	}

	return nil
}

// ValidateTokenRequest holds the request parameters for validating a token.
type ValidateTokenRequest struct {
	Token string `validate:"required"`
}

// ValidateToken checks if a token is valid.
func (s *Service) ValidateToken(ctx context.Context, token string) error {
	// Validate the request
	if err := s.validator.Struct(&ValidateTokenRequest{
		Token: token,
	}); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}

	if err := s.repo.ValidateToken(ctx, token); err != nil {
		return fmt.Errorf("%w", err)
	}

	return nil
}
