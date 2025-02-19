//go:generate go run go.uber.org/mock/mockgen@v0.5.0 -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

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

	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Service provides user authentication operations.
type Service interface {
	Register(ctx context.Context, email, password string) (string, string, error)
	Login(ctx context.Context, email, password string) (string, string, error)
}

// Config represents the users-service configuration.
type Config struct {
	Deadline time.Duration
}

// Users represents the users-service.
type Users struct {
	pb.UnimplementedUsersServiceServer
	logger *zap.Logger
	tp     trace.Tracer
	cfg    *Config
	svc    Service
}

// audienceInterceptor sets the audience in the context.
func audienceInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract the audience from metadata.
		audience, err := auth.AudienceFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithAudience(ctx, audience), req)
	}
}

// tokenInterceptor sets the token in the context.
func tokenInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Skip the interceptor for Register and Login methods.
		if strings.Contains(info.FullMethod, "Register") ||
			strings.Contains(info.FullMethod, "Login") {
			return handler(ctx, req)
		}

		// Extract the token from metadata.
		token, err := auth.AuthorizationTokenFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithAuthorizationToken(ctx, token), req)
	}
}

// roleInterceptor sets the role in the context.
func roleInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract the role from metadata.
		role, err := auth.RoleFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithRole(ctx, role), req)
	}
}

// New creates a new authentication server.
func New(ctx context.Context, cfg *Config, svc Service) *grpc.Server {
	server := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			audienceInterceptor(),
			tokenInterceptor(),
			roleInterceptor(),
		),
	)
	pb.RegisterUsersServiceServer(server, &Users{
		logger: loggerpkg.FromContext(ctx),
		tp:     otel.Tracer(svcpkg.Info().GetName()),
		cfg:    cfg,
		svc:    svc,
	})

	reflection.Register(server)
	return server
}

// Register registers a new user.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (u *Users) Register(ctx context.Context, req *pb.RegisterRequest) (res *pb.RegisterResponse, err error) {
	ctx, span := u.tp.Start(
		ctx,
		"App.Register",
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

	userID, token, err := u.svc.Register(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		u.logger.Error(
			"failed to register user",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	// Append the token in the headers.
	if err = grpc.SendHeader(ctx, auth.WithAuthorizationTokenAsHeaders(token)); err != nil {
		u.logger.Error(
			"failed to send token in headers",
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
	return &pb.RegisterResponse{}, nil
}

// Login logs in the user.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (u *Users) Login(ctx context.Context, req *pb.LoginRequest) (res *pb.LoginResponse, err error) {
	ctx, span := u.tp.Start(
		ctx,
		"App.Login",
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

	userID, token, err := u.svc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		u.logger.Error(
			"failed to login user",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	// Append the token in the headers.
	if err = grpc.SendHeader(ctx, auth.WithAuthorizationTokenAsHeaders(token)); err != nil {
		u.logger.Error(
			"failed to send token in headers",
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
	return &pb.LoginResponse{}, nil
}
