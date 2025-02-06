//go:generate go run go.uber.org/mock/mockgen@v0.5.0 -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package auth

import (
	"context"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides user authentication operations.
type Repository interface {
	Login(ctx context.Context, email, password string) (string, string, error)
	Register(ctx context.Context, email, password string) (string, string, error)
	Logout(ctx context.Context, token string) (string, error)
	Validate(ctx context.Context, token string) (string, error)
}

// Service provides user authentication operations.
type Service struct {
	validator *validator.Validate
	tp        trace.Tracer
	repo      Repository
}

// New creates a new auth service.
func New(validator *validator.Validate, repo Repository) *Service {
	serviceName := svcpkg.Info().GetName()
	return &Service{
		validator: validator,
		tp:        otel.Tracer(serviceName),
		repo:      repo,
	}
}

// RegisterRequest holds the request parameters for registering a new user.
type RegisterRequest struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required,min=8"`
}

// Register a new user.
func (s *Service) Register(ctx context.Context, email, password string) (userID, pat string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.Register")
	defer func() {
		span.RecordError(err)
		span.End()
	}()

	// Validate the request
	if err := s.validator.Struct(&RegisterRequest{
		Email:    email,
		Password: password,
	}); err != nil {
		return "", "", status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}

	return s.repo.Register(ctx, email, password)
}

// LoginRequest holds the request parameters for logging in a user.
type LoginRequest struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required,min=8"`
}

// Login user.
func (s *Service) Login(ctx context.Context, email, password string) (userID, pat string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.Login")
	defer func() {
		span.RecordError(err)
		span.End()
	}()

	// Validate the request
	if err := s.validator.Struct(&LoginRequest{
		Email:    email,
		Password: password,
	}); err != nil {
		return "", "", status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}

	return s.repo.Login(ctx, email, password)
}

// LogoutRequest holds the request parameters for logging out a user.
type LogoutRequest struct {
	Token string `validate:"required"`
}

// Logout user.
func (s *Service) Logout(ctx context.Context, token string) (userID string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.Logout")
	defer func() {
		span.RecordError(err)
		span.End()
	}()

	// Validate the request
	if err := s.validator.Struct(&LogoutRequest{
		Token: token,
	}); err != nil {
		return "", status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}

	return s.repo.Logout(ctx, token)
}

// ValidateRequest holds the request parameters for validating a token.
type ValidateRequest struct {
	Token string `validate:"required"`
}

// Validate checks if a token is valid.
func (s *Service) Validate(ctx context.Context, token string) (userID string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.Validate")
	defer func() {
		span.RecordError(err)
		span.End()
	}()

	// Validate the request
	if err := s.validator.Struct(&ValidateRequest{
		Token: token,
	}); err != nil {
		return "", status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}

	return s.repo.Validate(ctx, token)
}
