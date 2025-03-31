//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package users

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Service provides user related operations.
type Service interface {
	RegisterUser(ctx context.Context, req *userpb.RegisterUserRequest) (string, string, error)
	LoginUser(ctx context.Context, req *userpb.LoginUserRequest) (string, string, error)
}

// Config represents the users-service configuration.
type Config struct {
	Deadline    time.Duration
	Environment string
}

// Users represents the users-service.
type Users struct {
	userpb.UnimplementedUsersServiceServer
	logger *zap.Logger
	tp     trace.Tracer
	cfg    *Config
	svc    Service
}

// audienceInterceptor sets the audience from the metadata and adds it to the context.
func audienceInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the audience from metadata.
		audience, err := auth.ExtractAudienceFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithAudience(ctx, audience), req)
	}
}

// roleInterceptor extracts the role from the metadata and adds it to the context.
func roleInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the role from metadata.
		role, err := auth.ExtractRoleFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithRole(ctx, role), req)
	}
}

// authTokenInterceptor extracts the authToken from metadata and adds it to the context.
func authTokenInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Skip the interceptor if the method doesn't require authentication.
		if !isAuthRequired(info.FullMethod) {
			return handler(ctx, req)
		}

		// Extract the authToken from metadata.
		authToken, err := auth.ExtractAuthorizationTokenFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithAuthorizationToken(ctx, authToken), req)
	}
}

// isAuthRequired checks if the method requires authentication.
func isAuthRequired(method string) bool {
	authNotRequired := []string{
		"RegisterUser",
		"LoginUser",
	}

	for _, m := range authNotRequired {
		if strings.Contains(method, m) {
			return false
		}
	}

	return true
}

// isProduction checks if the environment is production.
func isProduction(environment string) bool {
	return strings.EqualFold(environment, "production")
}

// New creates a new users-service.
func New(ctx context.Context, cfg *Config, svc Service) *grpc.Server {
	server := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			audienceInterceptor(),
			roleInterceptor(),
			authTokenInterceptor(),
		),
	)
	userpb.RegisterUsersServiceServer(server, &Users{
		logger: loggerpkg.FromContext(ctx),
		tp:     otel.Tracer(svcpkg.Info().GetName()),
		cfg:    cfg,
		svc:    svc,
	})

	// Only register reflection for non-production environments.
	if !isProduction(cfg.Environment) {
		reflection.Register(server)
	}
	return server
}

// RegisterUser registers a new user.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (u *Users) RegisterUser(ctx context.Context, req *userpb.RegisterUserRequest) (res *userpb.RegisterUserResponse, err error) {
	ctx, span := u.tp.Start(
		ctx,
		"App.RegisterUser",
		trace.WithAttributes(attribute.String("email", req.GetEmail())),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, u.cfg.Deadline)
	defer cancel()

	userID, authToken, err := u.svc.RegisterUser(ctx, req)
	if err != nil {
		u.logger.Error(
			"failed to register user",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	// Append the authToken in the headers.
	if err = grpc.SendHeader(ctx, auth.WithSetAuthorizationTokenInHeaders(authToken)); err != nil {
		u.logger.Error(
			"failed to send authToken in headers",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	u.logger.Info(
		"user registered successfully",
		zap.Any("ctx", ctx),
		zap.String("user_id", userID),
	)
	return &userpb.RegisterUserResponse{UserId: userID}, nil
}

// LoginUser logs in the user.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (u *Users) LoginUser(ctx context.Context, req *userpb.LoginUserRequest) (res *userpb.LoginUserResponse, err error) {
	ctx, span := u.tp.Start(
		ctx,
		"App.LoginUser",
		trace.WithAttributes(attribute.String("email", req.GetEmail())),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, u.cfg.Deadline)
	defer cancel()

	userID, authToken, err := u.svc.LoginUser(ctx, req)
	if err != nil {
		u.logger.Error(
			"failed to login user",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	// Append the authToken in the headers.
	if err = grpc.SendHeader(ctx, auth.WithSetAuthorizationTokenInHeaders(authToken)); err != nil {
		u.logger.Error(
			"failed to send authToken in headers",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	u.logger.Info(
		"user logged in successfully",
		zap.Any("ctx", ctx),
		zap.String("user_id", userID),
	)
	return &userpb.LoginUserResponse{UserId: userID}, nil
}
