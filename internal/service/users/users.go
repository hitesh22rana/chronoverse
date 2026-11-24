//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package users

import (
	"context"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/trustelem/zxcvbn"
	"github.com/trustelem/zxcvbn/frequency"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

// Repository provides user related operations.
type Repository interface {
	RegisterUser(ctx context.Context, email, password, idempotencyKey string) (*usersmodel.GetUserResponse, string, error)
	LoginUser(ctx context.Context, email, password string) (*usersmodel.GetUserResponse, string, error)
	GetUser(ctx context.Context, id string) (*usersmodel.GetUserResponse, error)
	UpdateUser(ctx context.Context, id, notificationPreference string) error
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

// normalizeEmail lowercases and trims email for case-insensitive uniqueness.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// commonPasswords is the bundled zxcvbn dictionary; exact matches are rejected
// outright (all of them also score below 3 today — this holds if scoring changes).
var commonPasswords = toPasswordSet(frequency.FrequencyLists["passwords"])

func toPasswordSet(passwords []string) map[string]struct{} {
	set := make(map[string]struct{}, len(passwords))
	for _, password := range passwords {
		set[strings.ToLower(password)] = struct{}{}
	}
	return set
}

// weakPassword rejects bundled-dictionary matches and passwords scoring below 3.
func weakPassword(password, email string) bool {
	if _, ok := commonPasswords[strings.ToLower(password)]; ok {
		return true
	}
	return zxcvbn.PasswordStrength(password, []string{email}).Score < 3
}

// RegisterUserRequest holds the request parameters for registering a new user.
type RegisterUserRequest struct {
	Email          string `validate:"required,email"`
	Password       string `validate:"required,min=8,max=72"`
	IdempotencyKey string `validate:"required"`
}

// RegisterUser registers a new user and returns its ID with an auth token.
func (s *Service) RegisterUser(ctx context.Context, req *userpb.RegisterUserRequest) (userID, authToken string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.RegisterUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&RegisterUserRequest{
		Email:          normalizeEmail(req.GetEmail()),
		Password:       req.GetPassword(),
		IdempotencyKey: req.GetIdempotencyKey(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", "", err
	}

	if weakPassword(req.GetPassword(), normalizeEmail(req.GetEmail())) {
		return "", "", status.Errorf(codes.InvalidArgument, "password too weak")
	}

	res, authToken, err := s.repo.RegisterUser(ctx, normalizeEmail(req.GetEmail()), req.GetPassword(), req.GetIdempotencyKey())
	if err != nil {
		return "", "", err
	}

	return res.ID, authToken, nil
}

// LoginUserRequest holds the request parameters for logging in a user.
type LoginUserRequest struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required,min=8,max=72"`
}

// LoginUser authenticates a user and returns its ID with a fresh auth token.
func (s *Service) LoginUser(ctx context.Context, req *userpb.LoginUserRequest) (userID, authToken string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.LoginUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&LoginUserRequest{
		Email:    normalizeEmail(req.GetEmail()),
		Password: req.GetPassword(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", "", err
	}

	res, authToken, err := s.repo.LoginUser(ctx, normalizeEmail(req.GetEmail()), req.GetPassword())
	if err != nil {
		return "", "", normalizeLoginError(err)
	}

	return res.ID, authToken, nil
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

	err = s.validator.Struct(&GetUserRequest{
		ID: req.GetId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, err
	}

	return s.repo.GetUser(ctx, req.GetId())
}

// UpdateUserRequest holds the request parameters for updating a user.
type UpdateUserRequest struct {
	ID                     string `validate:"required"`
	NotificationPreference string `validate:"required,oneof=ALERTS ALL NONE"`
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

	err = s.validator.Struct(&UpdateUserRequest{
		ID:                     req.GetId(),
		NotificationPreference: req.GetNotificationPreference(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	return s.repo.UpdateUser(ctx, req.GetId(), req.GetNotificationPreference())
}

func normalizeLoginError(err error) error {
	code := status.Code(err)
	if code == codes.NotFound || code == codes.InvalidArgument {
		return status.Error(codes.Unauthenticated, "invalid email or password")
	}

	return err
}
