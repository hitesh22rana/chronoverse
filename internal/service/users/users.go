//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package users

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

// Repository provides user related operations.
type Repository interface {
	Register(ctx context.Context, email, password string) (string, string, error)
	Login(ctx context.Context, email, password string) (string, string, error)
}

// Service provides user related operations.
type Service struct {
	validator *validator.Validate
	tp        trace.Tracer
	repo      Repository
}

// New creates a new users-service.
func New(validator *validator.Validate, repo Repository) *Service {
	return &Service{
		validator: validator,
		tp:        otel.Tracer(svcpkg.Info().GetName()),
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
