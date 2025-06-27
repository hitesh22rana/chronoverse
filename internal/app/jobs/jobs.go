//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package jobs

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

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	authpkg "github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	grpcmiddlewares "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/middlewares"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// internalAPIs contains the list of internal APIs that require admin role.
// These APIs are not exposed to the public and should only be used internally.
var internalAPIs = map[string]bool{
	"ScheduleJob":     true,
	"UpdateJobStatus": true,
	"GetJobByID":      true,
}

// Service provides job related operations.
type Service interface {
	ScheduleJob(ctx context.Context, req *jobspb.ScheduleJobRequest) (string, error)
	UpdateJobStatus(ctx context.Context, req *jobspb.UpdateJobStatusRequest) error
	GetJob(ctx context.Context, req *jobspb.GetJobRequest) (*jobsmodel.GetJobResponse, error)
	GetJobByID(ctx context.Context, req *jobspb.GetJobByIDRequest) (*jobsmodel.GetJobByIDResponse, error)
	GetJobLogs(ctx context.Context, req *jobspb.GetJobLogsRequest) (*jobsmodel.GetJobLogsResponse, error)
	ListJobs(ctx context.Context, req *jobspb.ListJobsRequest) (*jobsmodel.ListJobsResponse, error)
}

// TLSConfig holds the TLS configuration for gRPC server.
type TLSConfig struct {
	Enabled  bool
	CAFile   string
	CertFile string
	KeyFile  string
}

// Config represents the jobs-service configuration.
type Config struct {
	Deadline    time.Duration
	Environment string
	TLSConfig   *TLSConfig
}

// Jobs represents the jobs-service.
type Jobs struct {
	jobspb.UnimplementedJobsServiceServer
	tp   trace.Tracer
	auth authpkg.IAuth
	cfg  *Config
	svc  Service
}

// authTokenInterceptor extracts and validates the authToken from the metadata and adds it to the context.
func (j *Jobs) authTokenInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Skip the interceptor if the method is a health check route.
		if isHealthCheckRoute(info.FullMethod) {
			return handler(ctx, req)
		}

		// Extract the authToken from metadata.
		authToken, err := authpkg.ExtractAuthorizationTokenFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		ctx = authpkg.WithAuthorizationToken(ctx, authToken)
		if _, err := j.auth.ValidateToken(ctx); err != nil {
			return "", err
		}

		return handler(ctx, req)
	}
}

// isHealthCheckRoute checks if the method is a health check route.
func isHealthCheckRoute(method string) bool {
	return strings.Contains(method, grpc_health_v1.Health_ServiceDesc.ServiceName)
}

// isInternalAPI checks if the full method is an internal API.
func isInternalAPI(fullMethod string) bool {
	parts := strings.Split(fullMethod, "/")
	if len(parts) < 3 {
		return false
	}

	return internalAPIs[parts[2]]
}

// isProduction checks if the environment is production.
func isProduction(environment string) bool {
	return strings.EqualFold(environment, "production")
}

// New creates a new jobs server.
func New(ctx context.Context, cfg *Config, auth authpkg.IAuth, svc Service) *grpc.Server {
	jobs := &Jobs{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: auth,
		cfg:  cfg,
		svc:  svc,
	}

	var serverOpts []grpc.ServerOption
	serverOpts = append(serverOpts,
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			grpcmiddlewares.LoggingInterceptor(loggerpkg.FromContext(ctx)),
			grpcmiddlewares.AudienceInterceptor(),
			grpcmiddlewares.RoleInterceptor(func(method, role string) bool {
				return isInternalAPI(method) && role != authpkg.RoleAdmin.String()
			}),
			jobs.authTokenInterceptor(),
		),
	)

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

	server := grpc.NewServer(serverOpts...)
	jobspb.RegisterJobsServiceServer(server, jobs)

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

// ScheduleJob schedules a job.
// This is an internal method used by internal services, and it should not be exposed to the public.
func (j *Jobs) ScheduleJob(ctx context.Context, req *jobspb.ScheduleJobRequest) (res *jobspb.ScheduleJobResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.ScheduleJob",
		trace.WithAttributes(
			attribute.String("workflow_id", req.GetWorkflowId()),
			attribute.String("user_id", req.GetUserId()),
			attribute.String("scheduled_at", req.GetScheduledAt()),
		),
	)

	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()

	jobID, err := j.svc.ScheduleJob(ctx, req)
	if err != nil {
		return nil, err
	}

	return &jobspb.ScheduleJobResponse{Id: jobID}, nil
}

// UpdateJobStatus updates the job status.
// This is an internal method used by internal services, and it should not be exposed to the public.
func (j *Jobs) UpdateJobStatus(
	ctx context.Context,
	req *jobspb.UpdateJobStatusRequest,
) (res *jobspb.UpdateJobStatusResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.UpdateJobStatus",
		trace.WithAttributes(
			attribute.String("id", req.GetId()),
			attribute.String("status", req.GetStatus()),
		),
	)

	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()

	err = j.svc.UpdateJobStatus(ctx, req)
	if err != nil {
		return nil, err
	}

	return &jobspb.UpdateJobStatusResponse{}, nil
}

// GetJob returns the job details by ID, job ID, and user ID.
func (j *Jobs) GetJob(ctx context.Context, req *jobspb.GetJobRequest) (res *jobspb.GetJobResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.GetJob",
		trace.WithAttributes(
			attribute.String("id", req.GetId()),
			attribute.String("workflow_id", req.GetWorkflowId()),
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

	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()

	job, err := j.svc.GetJob(ctx, req)
	if err != nil {
		return nil, err
	}

	return job.ToProto(), nil
}

// GetJobByID returns the job details by ID.
// This is an internal method used by internal services, and it should not be exposed to the public.
func (j *Jobs) GetJobByID(ctx context.Context, req *jobspb.GetJobByIDRequest) (res *jobspb.GetJobByIDResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.GetJobByID",
		trace.WithAttributes(
			attribute.String("id", req.GetId()),
		),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()

	job, err := j.svc.GetJobByID(ctx, req)
	if err != nil {
		return nil, err
	}

	return job.ToProto(), nil
}

// GetJobLogs returns the logs for the job.
func (j *Jobs) GetJobLogs(ctx context.Context, req *jobspb.GetJobLogsRequest) (res *jobspb.GetJobLogsResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.GetJobLogs",
		trace.WithAttributes(
			attribute.String("id", req.GetId()),
			attribute.String("workflow_id", req.GetWorkflowId()),
			attribute.String("user_id", req.GetUserId()),
			attribute.String("cursor", req.GetCursor()),
		),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()

	logs, err := j.svc.GetJobLogs(ctx, req)
	if err != nil {
		return nil, err
	}

	return logs.ToProto(), nil
}

// ListJobs returns the jobs by job ID.
func (j *Jobs) ListJobs(ctx context.Context, req *jobspb.ListJobsRequest) (res *jobspb.ListJobsResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.ListJobs",
		trace.WithAttributes(
			attribute.String("workflow_id", req.GetWorkflowId()),
			attribute.String("user_id", req.GetUserId()),
			attribute.String("cursor", req.GetCursor()),
		),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()

	jobs, err := j.svc.ListJobs(ctx, req)
	if err != nil {
		return nil, err
	}

	return jobs.ToProto(), nil
}
