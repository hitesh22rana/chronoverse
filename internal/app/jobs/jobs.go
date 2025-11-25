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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

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
	StreamJobLogs(ctx context.Context, req *jobspb.StreamJobLogsRequest) (chan *jobsmodel.JobLog, error)
	SearchJobLogs(ctx context.Context, req *jobspb.SearchJobLogsRequest) (*jobsmodel.GetJobLogsResponse, error)
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

// unaryAuthTokenInterceptor extracts and validates the authToken from the metadata and adds it to the context for the unary RPC calls.
func (j *Jobs) unaryAuthTokenInterceptor() grpc.UnaryServerInterceptor {
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

// streamAuthTokenInterceptor extracts and validates the authToken from the metadata and adds it to the context for the streaming RPC calls.
func (j *Jobs) streamAuthTokenInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Extract the authToken from metadata.
		authToken, err := authpkg.ExtractAuthorizationTokenFromMetadata(stream.Context())
		if err != nil {
			return err
		}

		ctx := authpkg.WithAuthorizationToken(stream.Context(), authToken)
		if _, err := j.auth.ValidateToken(ctx); err != nil {
			return err
		}

		return handler(srv, &grpcmiddlewares.WrappedServerStream{
			ServerStream: stream,
			Ctx:          ctx,
		})
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
			grpcmiddlewares.UnaryLoggingInterceptor(loggerpkg.FromContext(ctx)),
			grpcmiddlewares.UnaryAudienceInterceptor(),
			grpcmiddlewares.UnaryRoleInterceptor(func(method, role string) bool {
				return isInternalAPI(method) && role != authpkg.RoleAdmin.String()
			}),
			jobs.unaryAuthTokenInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			grpcmiddlewares.StreamLoggingInterceptor(loggerpkg.FromContext(ctx)),
			grpcmiddlewares.StreamAudienceInterceptor(),
			//nolint:contextcheck // This is a wrapper around grpc.ServerStream that allows to modify the context.
			grpcmiddlewares.StreamRoleInterceptor(func(_, _ string) bool {
				return false
			}),
			//nolint:contextcheck // This is a wrapper around grpc.ServerStream that allows to modify the context.
			jobs.streamAuthTokenInterceptor(),
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
func (j *Jobs) ScheduleJob(ctx context.Context, req *jobspb.ScheduleJobRequest) (res *jobspb.ScheduleJobResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.ScheduleJob",
		trace.WithAttributes(
			attribute.String("workflow_id", req.GetWorkflowId()),
			attribute.String("user_id", req.GetUserId()),
			attribute.String("scheduled_at", req.GetScheduledAt()),
			attribute.String("trigger", req.GetTrigger()),
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
			attribute.String("container_id", req.GetContainerId()),
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
			attribute.String("filters.stream", req.GetFilters().GetStream().String()),
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

// StreamJobLogs streams the logs for the job.
func (j *Jobs) StreamJobLogs(req *jobspb.StreamJobLogsRequest, stream jobspb.JobsService_StreamJobLogsServer) (err error) {
	ctx, span := j.tp.Start(
		stream.Context(),
		"App.StreamJobLogs",
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

	ch, err := j.svc.StreamJobLogs(ctx, req)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			// Client disconnected or context canceled
			switch ctx.Err() {
			case context.Canceled:
				// Client disconnected - just return, don't send error
				return nil
			case context.DeadlineExceeded:
				// Context deadline exceeded
				// This can happen if the client takes too long to read the stream.
				return status.Error(codes.DeadlineExceeded, ctx.Err().Error())
			default:
				// Other context errors which can happen if the context is canceled or if there is an error in the context.
				return status.Error(codes.Unknown, ctx.Err().Error())
			}
		case data, ok := <-ch:
			if !ok {
				// Channel closed, stream ended
				return nil
			}

			if data == nil {
				continue
			}

			if err := stream.Send(data.ToProto()); err != nil {
				return err
			}
		}
	}
}

// SearchJobLogs returns the filtered logs of a job.
func (j *Jobs) SearchJobLogs(ctx context.Context, req *jobspb.SearchJobLogsRequest) (res *jobspb.GetJobLogsResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.SearchJobLogs",
		trace.WithAttributes(
			attribute.String("id", req.GetId()),
			attribute.String("workflow_id", req.GetWorkflowId()),
			attribute.String("user_id", req.GetUserId()),
			attribute.String("cursor", req.GetCursor()),
			attribute.String("filters.stream", req.GetFilters().GetStream().String()),
			attribute.String("filters.message", req.GetFilters().GetMessage()),
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

	logs, err := j.svc.SearchJobLogs(ctx, req)
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
			attribute.String("filters.status", req.GetFilters().GetStatus()),
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

	return jobs.ToProto(authpkg.IsInternalService(ctx)), nil
}
