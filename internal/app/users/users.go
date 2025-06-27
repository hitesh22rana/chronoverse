//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package users

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	grpcmiddlewares "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/middlewares"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Service provides user related operations.
type Service interface {
	RegisterUser(ctx context.Context, req *userpb.RegisterUserRequest) (string, string, error)
	LoginUser(ctx context.Context, req *userpb.LoginUserRequest) (string, string, error)
	GetUser(ctx context.Context, req *userpb.GetUserRequest) (*usersmodel.GetUserResponse, error)
	UpdateUser(ctx context.Context, req *userpb.UpdateUserRequest) error
}

// TLSConfig holds the TLS configuration for gRPC server.
type TLSConfig struct {
	Enabled  bool
	CAFile   string
	CertFile string
	KeyFile  string
}

// Config represents the users-service configuration.
type Config struct {
	Deadline    time.Duration
	Environment string
	TLSConfig   *TLSConfig
}

// Users represents the users-service.
type Users struct {
	userpb.UnimplementedUsersServiceServer
	tp   trace.Tracer
	auth auth.IAuth
	cfg  *Config
	svc  Service
}

// authTokenInterceptor extracts and validates the authToken from the metadata and adds it to the context.
func (u *Users) authTokenInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Skip the interceptor if the method is a health check route.
		if isHealthCheckRoute(info.FullMethod) {
			return handler(ctx, req)
		}

		// Skip the interceptor if the method doesn't require authentication.
		if !isAuthRequired(info.FullMethod) {
			return handler(ctx, req)
		}

		// Extract the authToken from metadata.
		authToken, err := auth.ExtractAuthorizationTokenFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		ctx = auth.WithAuthorizationToken(ctx, authToken)
		if _, err := u.auth.ValidateToken(ctx); err != nil {
			return "", err
		}

		return handler(ctx, req)
	}
}

// isHealthCheckRoute checks if the method is a health check route.
func isHealthCheckRoute(method string) bool {
	return strings.Contains(method, grpc_health_v1.Health_ServiceDesc.ServiceName)
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
func New(ctx context.Context, cfg *Config, _auth auth.IAuth, svc Service) *grpc.Server {
	users := &Users{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: _auth,
		cfg:  cfg,
		svc:  svc,
	}

	var serverOpts []grpc.ServerOption
	if cfg.TLSConfig != nil && cfg.TLSConfig.Enabled {
		// Load CA certificate
		caCert, err := os.ReadFile(cfg.TLSConfig.CAFile)
		if err != nil {
			loggerpkg.FromContext(ctx).Fatal(
				"failed to read CA certificate file",
				zap.Error(err),
				zap.String("ca_file", cfg.TLSConfig.CAFile),
			)
			return nil
		}

		caCertPool := x509.NewCertPool()
		if ok := caCertPool.AppendCertsFromPEM(caCert); !ok {
			loggerpkg.FromContext(ctx).Fatal(
				"failed to append CA certificate to pool",
				zap.String("ca_file", cfg.TLSConfig.CAFile),
				zap.Error(err),
			)
			return nil
		}

		// Server certificate and private key
		serverCert, err := tls.LoadX509KeyPair(cfg.TLSConfig.CertFile, cfg.TLSConfig.KeyFile)
		if err != nil {
			loggerpkg.FromContext(ctx).Fatal(
				"failed to load server certificate and key",
				zap.Error(err),
				zap.String("cert_file", cfg.TLSConfig.CertFile),
				zap.String("key_file", cfg.TLSConfig.KeyFile),
			)
			return nil
		}

		config := &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    caCertPool,
			MinVersion:   tls.VersionTLS12,
		}

		serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(config)))
	}

	serverOpts = append(serverOpts,
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			grpcmiddlewares.LoggingInterceptor(loggerpkg.FromContext(ctx)),
			grpcmiddlewares.AudienceInterceptor(),
			grpcmiddlewares.RoleInterceptor(func(_, _ string) bool {
				return false
			}),
			users.authTokenInterceptor(),
		),
	)

	server := grpc.NewServer(serverOpts...)
	userpb.RegisterUsersServiceServer(server, users)

	healthServer := health.NewServer()

	healthServer.SetServingStatus(
		svcpkg.Info().GetName(),
		grpc_health_v1.HealthCheckResponse_SERVING,
	)

	// Register the health server.
	grpc_health_v1.RegisterHealthServer(server, healthServer)

	// Only register reflection for non-production environments.
	if !isProduction(cfg.Environment) {
		reflection.Register(server)
	}
	return server
}

// RegisterUser registers a new user.
//
//nolint:dupl,gocritic // It's okay to have similar code for different methods.
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
		return nil, err
	}

	// Append the authToken in the headers.
	if err = grpc.SendHeader(ctx, auth.WithSetAuthorizationTokenInHeaders(authToken)); err != nil {
		return nil, err
	}

	return &userpb.RegisterUserResponse{UserId: userID}, nil
}

// LoginUser logs in the user.
//
//nolint:dupl,gocritic // It's okay to have similar code for different methods.
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
		return nil, err
	}

	// Append the authToken in the headers.
	if err = grpc.SendHeader(ctx, auth.WithSetAuthorizationTokenInHeaders(authToken)); err != nil {
		return nil, err
	}

	return &userpb.LoginUserResponse{UserId: userID}, nil
}

// GetUser gets the user.
func (u *Users) GetUser(ctx context.Context, req *userpb.GetUserRequest) (res *userpb.GetUserResponse, err error) {
	ctx, span := u.tp.Start(
		ctx,
		"App.GetUser",
		trace.WithAttributes(attribute.String("user_id", req.GetId())),
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

	user, err := u.svc.GetUser(ctx, req)
	if err != nil {
		return nil, err
	}

	return user.ToProto(), nil
}

// UpdateUser updates the user.
func (u *Users) UpdateUser(ctx context.Context, req *userpb.UpdateUserRequest) (res *userpb.UpdateUserResponse, err error) {
	ctx, span := u.tp.Start(
		ctx,
		"App.UpdateUser",
		trace.WithAttributes(attribute.String("user_id", req.GetId())),
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

	err = u.svc.UpdateUser(ctx, req)
	if err != nil {
		return nil, err
	}

	return &userpb.UpdateUserResponse{}, nil
}
