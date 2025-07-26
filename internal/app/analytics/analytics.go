//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package analytics

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
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	grpcmiddlewares "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/middlewares"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Service defines the interface for analytics service operations.
type Service interface {
	GetUserAnalytics(ctx context.Context, req *analyticspb.GetUserAnalyticsRequest) (*analyticsmodel.GetUserAnalyticsResponse, error)
	GetWorkflowAnalytics(ctx context.Context, req *analyticspb.GetWorkflowAnalyticsRequest) (*analyticsmodel.GetWorkflowAnalyticsResponse, error)
}

// TLSConfig holds TLS configuration for the gRPC server.
type TLSConfig struct {
	Enabled  bool   `json:"enabled"`
	CAFile   string `json:"ca_file"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// Config holds the analytics-service configuration.
type Config struct {
	Deadline    time.Duration `json:"deadline"`
	Environment string        `json:"environment"`
	TLSConfig   *TLSConfig    `json:"tls_config"`
}

// Analytics represents the analytics-service.
type Analytics struct {
	tp   trace.Tracer
	auth auth.IAuth
	cfg  *Config
	svc  Service
}

// authTokenInterceptor extracts and validates the authToken from the metadata and adds it to the context.
func (a *Analytics) authTokenInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Skip the interceptor if the method is a health check route.
		if isHealthCheckRoute(info.FullMethod) {
			return handler(ctx, req)
		}

		// Extract the authToken from metadata.
		authToken, err := auth.ExtractAuthorizationTokenFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		ctx = auth.WithAuthorizationToken(ctx, authToken)
		if _, err := a.auth.ValidateToken(ctx); err != nil {
			return "", err
		}

		return handler(ctx, req)
	}
}

// isHealthCheckRoute checks if the method is a health check route.
func isHealthCheckRoute(method string) bool {
	return strings.Contains(method, grpc_health_v1.Health_ServiceDesc.ServiceName)
}

// isProduction checks if the environment is production.
func isProduction(environment string) bool {
	return strings.EqualFold(environment, "production")
}

// New creates a new analytics analytics-service.
func New(ctx context.Context, cfg *Config, auth auth.IAuth, svc Service) *grpc.Server {
	analytics := &Analytics{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: auth,
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
			grpcmiddlewares.UnaryLoggingInterceptor(loggerpkg.FromContext(ctx)),
			grpcmiddlewares.UnaryAudienceInterceptor(),
			grpcmiddlewares.UnaryRoleInterceptor(func(_, _ string) bool {
				return false
			}),
			analytics.authTokenInterceptor(),
		),
	)

	server := grpc.NewServer(serverOpts...)
	analyticspb.RegisterAnalyticsServiceServer(server, analytics)

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

// GetUserAnalytics retrieves analytics data for a specific user.
func (a *Analytics) GetUserAnalytics(ctx context.Context, req *analyticspb.GetUserAnalyticsRequest) (res *analyticspb.GetUserAnalyticsResponse, err error) {
	ctx, span := a.tp.Start(
		ctx, "App.GetUserAnalytics",
		trace.WithAttributes(
			attribute.String("user_id", req.GetUserId()),
		),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	analytics, err := a.svc.GetUserAnalytics(ctx, req)
	if err != nil {
		return nil, err
	}

	return analytics.ToProto(), nil
}

// GetWorkflowAnalytics retrieves analytics data for a specific workflow.
func (a *Analytics) GetWorkflowAnalytics(ctx context.Context, req *analyticspb.GetWorkflowAnalyticsRequest) (res *analyticspb.GetWorkflowAnalyticsResponse, err error) {
	ctx, span := a.tp.Start(
		ctx, "App.GetWorkflowAnalytics",
		trace.WithAttributes(
			attribute.String("user_id", req.GetUserId()),
			attribute.String("workflow_id", req.GetWorkflowId()),
		),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	analytics, err := a.svc.GetWorkflowAnalytics(ctx, req)
	if err != nil {
		return nil, err
	}

	return analytics.ToProto(), nil
}
