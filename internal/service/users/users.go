//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package users

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	defaultExpirationTTL = time.Minute * 30
	cacheTimeout         = time.Second * 2
)

// Repository provides user related operations.
type Repository interface {
	RegisterUser(ctx context.Context, email, password string) (*usersmodel.GetUserResponse, string, error)
	LoginUser(ctx context.Context, email, password string) (*usersmodel.GetUserResponse, string, error)
	GetUser(ctx context.Context, id string) (*usersmodel.GetUserResponse, error)
	UpdateUser(ctx context.Context, id, password, notificationPreference string) error
}

// Cache provides cache related operations.
type Cache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Get(ctx context.Context, key string, dest any) (any, error)
	Delete(ctx context.Context, key string) error
}

// Service provides user related operations.
type Service struct {
	validator *validator.Validate
	tp        trace.Tracer
	repo      Repository
	cache     Cache
}

// New creates a new users-service.
func New(validator *validator.Validate, repo Repository, cache Cache) *Service {
	return &Service{
		validator: validator,
		tp:        otel.Tracer(svcpkg.Info().GetName()),
		repo:      repo,
		cache:     cache,
	}
}

// RegisterUserRequest holds the request parameters for registering a new user.
type RegisterUserRequest struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required,min=8,max=100"`
}

// RegisterUser a new user.
//
//nolint:dupl // It's ok to have duplicate code here as the logic is similar to other methods.
func (s *Service) RegisterUser(ctx context.Context, req *userpb.RegisterUserRequest) (userID, authToken string, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.RegisterUser"),
	)
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

	res, authToken, err := s.repo.RegisterUser(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return "", "", err
	}

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
		defer cancel()

		// Cache the LoginUser response
		// The key is in the format "user:{user_id}"
		cacheKey := fmt.Sprintf("user:%s", res.ID)
		if setErr := s.cache.Set(bgCtx, cacheKey, res, defaultExpirationTTL); setErr != nil {
			logger.Warn("failed to cache user",
				zap.String("user_id", res.ID),
				zap.String("cache_key", cacheKey),
				zap.Error(setErr),
			)
		} else if logger.Core().Enabled(zap.DebugLevel) {
			logger.Debug("cached user",
				zap.String("user_id", res.ID),
				zap.String("cache_key", cacheKey),
			)
		}
	}()

	return res.ID, authToken, nil
}

// LoginUserRequest holds the request parameters for logging in a user.
type LoginUserRequest struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required,min=8,max=100"`
}

// LoginUser user.
//
//nolint:dupl // It's ok to have duplicate code here as the logic is similar to other methods.
func (s *Service) LoginUser(ctx context.Context, req *userpb.LoginUserRequest) (userID, authToken string, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.LoginUser"),
	)
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

	res, authToken, err := s.repo.LoginUser(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return "", "", err
	}

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
		defer cancel()

		// Cache the LoginUser response
		// The key is in the format "user:{user_id}"
		cacheKey := fmt.Sprintf("user:%s", res.ID)
		if setErr := s.cache.Set(bgCtx, cacheKey, res, defaultExpirationTTL); setErr != nil {
			logger.Warn("failed to cache user",
				zap.String("user_id", res.ID),
				zap.String("cache_key", cacheKey),
				zap.Error(setErr),
			)
		} else if logger.Core().Enabled(zap.DebugLevel) {
			logger.Debug("cached user",
				zap.String("user_id", res.ID),
				zap.String("cache_key", cacheKey),
			)
		}
	}()

	return res.ID, authToken, nil
}

// GetUserRequest holds the request parameters for getting a user.
type GetUserRequest struct {
	ID string `validate:"required"`
}

// GetUser gets a user.
func (s *Service) GetUser(ctx context.Context, req *userpb.GetUserRequest) (res *usersmodel.GetUserResponse, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.GetUser"),
	)
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

	// Check if the user is already cached
	cacheKey := fmt.Sprintf("user:%s", req.GetId())
	cacheRes, cacheErr := s.cache.Get(ctx, cacheKey, &usersmodel.GetUserResponse{})
	if cacheErr != nil {
		if errors.Is(cacheErr, context.DeadlineExceeded) || errors.Is(cacheErr, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, cacheErr.Error())
			return nil, err
		}
	} else {
		// Cache hit, return cached response
		//nolint:errcheck,forcetypeassert // Ignore error as we are just reading from cache
		return cacheRes.(*usersmodel.GetUserResponse), nil
	}

	res, err = s.repo.GetUser(ctx, req.GetId())
	if err != nil {
		return nil, err
	}

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
		defer cancel()

		// Cache the GetUser response
		// The key is in the format "user:{user_id}"
		if setErr := s.cache.Set(bgCtx, cacheKey, res, defaultExpirationTTL); setErr != nil {
			logger.Warn("failed to cache user",
				zap.String("user_id", res.ID),
				zap.String("cache_key", cacheKey),
				zap.Error(setErr),
			)
		} else if logger.Core().Enabled(zap.DebugLevel) {
			logger.Debug("cached user",
				zap.String("user_id", res.ID),
				zap.String("cache_key", cacheKey),
			)
		}
	}()

	return res, nil
}

// UpdateUserRequest holds the request parameters for updating a user.
type UpdateUserRequest struct {
	Password               string `validate:"required,min=8,max=100"`
	NotificationPreference string `validate:"required"`
}

// UpdateUser updates a user.
func (s *Service) UpdateUser(ctx context.Context, req *userpb.UpdateUserRequest) (err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.UpdateUser"),
	)
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
	if err != nil {
		return err
	}

	// Invalidate the cache in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
		defer cancel()

		// Invalidate the user cache
		// The key is in the format "user:{user_id}"
		cacheKey := fmt.Sprintf("user:%s", req.GetId())
		if delErr := s.cache.Delete(bgCtx, cacheKey); delErr != nil && status.Code(delErr) != codes.NotFound {
			logger.Warn("failed to delete user cache",
				zap.String("user_id", req.GetId()),
				zap.String("cache_key", cacheKey),
				zap.Error(delErr),
			)
		} else if logger.Core().Enabled(zap.DebugLevel) {
			logger.Debug("deleted user cache",
				zap.String("user_id", req.GetId()),
				zap.String("cache_key", cacheKey),
			)
		}
	}()

	return err
}
