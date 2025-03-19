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

	"github.com/hitesh22rana/chronoverse/internal/model"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Service provides job related operations.
type Service interface {
	CreateJob(ctx context.Context, req *jobspb.CreateJobRequest) (string, error)
	UpdateJob(ctx context.Context, req *jobspb.UpdateJobRequest) error
	UpdateJobBuildStatus(ctx context.Context, req *jobspb.UpdateJobBuildStatusRequest) error
	GetJob(ctx context.Context, req *jobspb.GetJobRequest) (*model.GetJobResponse, error)
	GetJobByID(ctx context.Context, req *jobspb.GetJobByIDRequest) (*model.GetJobByIDResponse, error)
	ScheduleJob(ctx context.Context, req *jobspb.ScheduleJobRequest) (string, error)
	UpdateScheduledJobStatus(ctx context.Context, req *jobspb.UpdateScheduledJobStatusRequest) error
	GetScheduledJobByID(ctx context.Context, req *jobspb.GetScheduledJobByIDRequest) (*model.GetScheduledJobByIDResponse, error)
	ListJobsByUserID(ctx context.Context, req *jobspb.ListJobsByUserIDRequest) (*model.ListJobsByUserIDResponse, error)
	ListScheduledJobs(ctx context.Context, req *jobspb.ListScheduledJobsRequest) (*model.ListScheduledJobsResponse, error)
}

// Config represents the jobs-service configuration.
type Config struct {
	Deadline time.Duration
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
	internalAPIs := []string{
		"GetJobByID",
		"UpdateJobBuildStatus",
		"ScheduleJob",
		"UpdateScheduledJobStatus",
		"GetScheduledJobByID",
	}

	for _, api := range internalAPIs {
		if strings.Contains(fullMethod, api) {
			return true
		}
	}

	return false
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

	reflection.Register(server)
	return server
}

// CreateJob creates a new job.
func (j *Jobs) CreateJob(ctx context.Context, req *jobspb.CreateJobRequest) (res *jobspb.CreateJobResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.CreateJob",
		trace.WithAttributes(
			attribute.String("user_id", req.GetUserId()),
			attribute.String("name", req.GetName()),
			attribute.String("payload", req.GetPayload()),
			attribute.String("kind", req.GetKind()),
			attribute.Int("interval", int(req.GetInterval())),
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

	jobID, err := j.svc.CreateJob(ctx, req)
	if err != nil {
		j.logger.Error(
			"failed to create job",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"job created successfully",
		zap.Any("ctx", ctx),
		zap.String("job_id", jobID),
	)
	return &jobspb.CreateJobResponse{Id: jobID}, nil
}

// UpdateJob updates the job details.
func (j *Jobs) UpdateJob(ctx context.Context, req *jobspb.UpdateJobRequest) (res *jobspb.UpdateJobResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.UpdateJob",
		trace.WithAttributes(
			attribute.String("id", req.GetId()),
			attribute.String("user_id", req.GetUserId()),
			attribute.String("name", req.GetName()),
			attribute.String("payload", req.GetPayload()),
			attribute.Int("interval", int(req.GetInterval())),
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

	err = j.svc.UpdateJob(ctx, req)
	if err != nil {
		j.logger.Error(
			"failed to update job",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"job updated successfully",
		zap.Any("ctx", ctx),
	)
	return &jobspb.UpdateJobResponse{}, nil
}

// UpdateJobBuildStatus updates the job build status.
// This is an internal method used by internal services, and it should not be exposed to the public.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (j *Jobs) UpdateJobBuildStatus(ctx context.Context, req *jobspb.UpdateJobBuildStatusRequest) (res *jobspb.UpdateJobBuildStatusResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.UpdateJobBuildStatus",
		trace.WithAttributes(
			attribute.String("id", req.GetId()),
			attribute.String("build_status", req.GetBuildStatus()),
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

	err = j.svc.UpdateJobBuildStatus(ctx, req)
	if err != nil {
		j.logger.Error(
			"failed to update job build status",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"job build status updated successfully",
		zap.Any("ctx", ctx),
	)
	return &jobspb.UpdateJobBuildStatusResponse{}, nil
}

// GetJob returns the job details by ID and user ID.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (j *Jobs) GetJob(ctx context.Context, req *jobspb.GetJobRequest) (res *jobspb.GetJobResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.GetJob",
		trace.WithAttributes(
			attribute.String("id", req.GetId()),
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
//
//nolint:dupl // It's okay to have similar code for different methods.
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

// ScheduleJob schedules a job.
// This is an internal method used by internal services, and it should not be exposed to the public.
func (j *Jobs) ScheduleJob(ctx context.Context, req *jobspb.ScheduleJobRequest) (res *jobspb.ScheduleJobResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.ScheduleJob",
		trace.WithAttributes(
			attribute.String("job_id", req.GetJobId()),
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

	scheduledJobID, err := j.svc.ScheduleJob(ctx, req)
	if err != nil {
		j.logger.Error(
			"failed to schedule job",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"job scheduled successfully",
		zap.Any("ctx", ctx),
		zap.String("scheduled_job_id", scheduledJobID),
	)
	return &jobspb.ScheduleJobResponse{Id: scheduledJobID}, nil
}

// UpdateScheduledJobStatus updates the scheduled job status.
// This is an internal method used by internal services, and it should not be exposed to the public.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (j *Jobs) UpdateScheduledJobStatus(
	ctx context.Context,
	req *jobspb.UpdateScheduledJobStatusRequest,
) (res *jobspb.UpdateScheduledJobStatusResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.UpdateScheduledJobStatus",
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

	err = j.svc.UpdateScheduledJobStatus(ctx, req)
	if err != nil {
		j.logger.Error(
			"failed to update scheduled job status",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"scheduled job status updated successfully",
		zap.Any("ctx", ctx),
	)
	return &jobspb.UpdateScheduledJobStatusResponse{}, nil
}

// GetScheduledJobByID returns the scheduled job details by ID.
// This is an internal method used by internal services, and it should not be exposed to the public.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (j *Jobs) GetScheduledJobByID(ctx context.Context, req *jobspb.GetScheduledJobByIDRequest) (res *jobspb.GetScheduledJobByIDResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.GetScheduledJobByID",
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

	scheduledJob, err := j.svc.GetScheduledJobByID(ctx, req)
	if err != nil {
		j.logger.Error(
			"failed to fetch scheduled job details",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"scheduled job details fetched successfully",
		zap.Any("ctx", ctx),
		zap.Any("scheduled_job", scheduledJob),
	)

	return scheduledJob.ToProto(), nil
}

// ListJobsByUserID returns the jobs by user ID.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (j *Jobs) ListJobsByUserID(ctx context.Context, req *jobspb.ListJobsByUserIDRequest) (res *jobspb.ListJobsByUserIDResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.ListJobsByUserID",
		trace.WithAttributes(
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

	jobs, err := j.svc.ListJobsByUserID(ctx, req)
	if err != nil {
		j.logger.Error(
			"failed to fetch all jobs by user ID",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"jobs details fetched successfully",
		zap.Any("ctx", ctx),
		zap.Any("jobs", jobs),
	)
	return jobs.ToProto(), nil
}

// ListScheduledJobs returns the scheduled jobs by job ID.
func (j *Jobs) ListScheduledJobs(ctx context.Context, req *jobspb.ListScheduledJobsRequest) (res *jobspb.ListScheduledJobsResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.ListScheduledJobs",
		trace.WithAttributes(
			attribute.String("job_id", req.GetJobId()),
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

	scheduledJobs, err := j.svc.ListScheduledJobs(ctx, req)
	if err != nil {
		j.logger.Error(
			"failed to fetch scheduled jobs by JobID",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"scheduled jobs fetched successfully",
		zap.Any("ctx", ctx),
		zap.Any("scheduled_jobs", scheduledJobs),
	)
	return scheduledJobs.ToProto(), nil
}
