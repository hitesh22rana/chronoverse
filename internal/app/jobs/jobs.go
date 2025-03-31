//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package jobs

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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
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

// Config represents the jobs-service configuration.
type Config struct {
	Deadline    time.Duration
	Environment string
}

// Jobs represents the jobs-service.
type Jobs struct {
	jobspb.UnimplementedJobsServiceServer
	logger *zap.Logger
	tp     trace.Tracer
	auth   auth.IAuth
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
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the role from metadata.
		role, err := auth.ExtractRoleFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		// Validate the role for internal APIs.
		if isInternalAPI(info.FullMethod) && role != auth.RoleAdmin.String() {
			return "", status.Error(codes.PermissionDenied, "unauthorized access")
		}

		return handler(auth.WithRole(ctx, role), req)
	}
}

// authTokenInterceptor extracts the authToken from metadata and adds it to the context.
func (j *Jobs) authTokenInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the authToken from metadata.
		authToken, err := auth.ExtractAuthorizationTokenFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		ctx = auth.WithAuthorizationToken(ctx, authToken)
		if _, err := j.auth.ValidateToken(ctx); err != nil {
			return "", err
		}

		return handler(ctx, req)
	}
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
func New(ctx context.Context, cfg *Config, auth auth.IAuth, svc Service) *grpc.Server {
	jobs := &Jobs{
		logger: loggerpkg.FromContext(ctx),
		tp:     otel.Tracer(svcpkg.Info().GetName()),
		auth:   auth,
		cfg:    cfg,
		svc:    svc,
	}

	server := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			audienceInterceptor(),
			roleInterceptor(),
			jobs.authTokenInterceptor(),
		),
	)
	jobspb.RegisterJobsServiceServer(server, jobs)

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
		j.logger.Error(
			"failed to schedule job",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"job successfully",
		zap.Any("ctx", ctx),
		zap.String("job_id", jobID),
	)
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
		j.logger.Error(
			"failed to update job status",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"job status updated successfully",
		zap.Any("ctx", ctx),
	)
	return &jobspb.UpdateJobStatusResponse{}, nil
}

// GetJob returns the job details by ID, job ID, and user ID.
//
//nolint:dupl // It's okay to have similar code for different methods.
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
		j.logger.Error(
			"failed to fetch job details",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"job details fetched successfully",
		zap.Any("ctx", ctx),
		zap.Any("job", job),
	)

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
		j.logger.Error(
			"failed to fetch job details",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"job details fetched successfully",
		zap.Any("ctx", ctx),
		zap.Any("job", job),
	)
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
		j.logger.Error(
			"failed to fetch job logs",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"job logs fetched successfully",
		zap.Any("ctx", ctx),
		zap.Any("logs", logs),
	)
	return logs.ToProto(), nil
}

// ListJobs returns the jobs by job ID.
//
//nolint:dupl // It's okay to have similar code for different methods.
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
		j.logger.Error(
			"failed to fetch jobs",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"jobs fetched successfully",
		zap.Any("ctx", ctx),
		zap.Any("jobs", jobs),
	)
	return jobs.ToProto(), nil
}
