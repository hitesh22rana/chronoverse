//go:generate go run go.uber.org/mock/mockgen@v0.5.0 -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package auth

import (
	"context"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides user authentication operations.
type Repository interface {
	Login(ctx context.Context, email, password string) (string, string, error)
	Register(ctx context.Context, email, password string) (string, string, error)
	Logout(ctx context.Context) (string, error)
	Validate(ctx context.Context) (string, error)
}

// Service provides user authentication operations.
type Service struct {
	validator *validator.Validate
	tp        trace.Tracer
	repo      Repository
}

// New creates a new auth service.
func New(validator *validator.Validate, repo Repository) *Service {
	serviceName := svcpkg.Info().GetServiceInfo()
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
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&RegisterRequest{
		Email:    email,
		Password: password,
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", "", err
	}

	userID, pat, err = s.repo.Register(ctx, email, password)
	if err != nil {
		return "", "", err
	}

	return userID, pat, nil
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
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&LoginRequest{
		Email:    email,
		Password: password,
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", "", err
	}

	userID, pat, err = s.repo.Login(ctx, email, password)
	if err != nil {
		return "", "", err
	}

	return userID, pat, nil
}

// Logout user.
func (s *Service) Logout(ctx context.Context) (userID string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.Logout")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	userID, err = s.repo.Logout(ctx)
	if err != nil {
		return "", err
	}

	return userID, nil
}

// Validate checks if a token is valid.
func (s *Service) Validate(ctx context.Context) (userID string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.Validate")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	userID, err = s.repo.Validate(ctx)
	if err != nil {
		return "", err
	}

	return userID, nil
}
