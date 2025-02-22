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

	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"

	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides user related operations.
type Repository interface {
	RegisterUser(ctx context.Context, email, password string) (string, string, error)
	LoginUser(ctx context.Context, email, password string) (string, string, error)
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

// RegisterUserRequest holds the request parameters for registering a new user.
type RegisterUserRequest struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required,min=8"`
}

// RegisterUser a new user.
func (s *Service) RegisterUser(ctx context.Context, req *userpb.RegisterUserRequest) (userID, authToken string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.RegisterUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&RegisterUserRequest{
		Email:    req.GetEmail(),
		Password: req.GetPassword(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", "", err
	}

	userID, authToken, err = s.repo.RegisterUser(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return "", "", err
	}

	return userID, authToken, nil
}

// LoginUserRequest holds the request parameters for logging in a user.
type LoginUserRequest struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required,min=8"`
}

// LoginUser user.
func (s *Service) LoginUser(ctx context.Context, req *userpb.LoginUserRequest) (userID, authToken string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.LoginUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&LoginUserRequest{
		Email:    req.GetEmail(),
		Password: req.GetPassword(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", "", err
	}

	userID, authToken, err = s.repo.LoginUser(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return "", "", err
	}

	return userID, authToken, nil
}
