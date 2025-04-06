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

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides user related operations.
type Repository interface {
	RegisterUser(ctx context.Context, email, password string) (string, string, error)
	LoginUser(ctx context.Context, email, password string) (string, string, error)
	GetUser(ctx context.Context, id string) (*usersmodel.GetUserResponse, error)
	UpdateUser(ctx context.Context, id, password, notificationPreference string) error
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

// GetUserRequest holds the request parameters for getting a user.
type GetUserRequest struct {
	ID string `validate:"required"`
}

// GetUser gets a user.
func (s *Service) GetUser(ctx context.Context, req *userpb.GetUserRequest) (res *usersmodel.GetUserResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.GetUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&GetUserRequest{
		ID: req.GetId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, err
	}

	res, err = s.repo.GetUser(ctx, req.GetId())
	if err != nil {
		return nil, err
	}

	return res, nil
}

// UpdateUserRequest holds the request parameters for updating a user.
type UpdateUserRequest struct {
	Password               string `validate:"required,min=8"`
	NotificationPreference string `validate:"required"`
}

// UpdateUser updates a user.
func (s *Service) UpdateUser(ctx context.Context, req *userpb.UpdateUserRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.UpdateUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&UpdateUserRequest{
		Password:               req.GetPassword(),
		NotificationPreference: req.GetNotificationPreference(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	err = s.repo.UpdateUser(
		ctx,
		req.GetId(),
		req.GetPassword(),
		req.GetNotificationPreference(),
	)

	return err
}
