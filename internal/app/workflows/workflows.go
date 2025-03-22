//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package workflows

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

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Service provides workflow related operations.
type Service interface {
	CreateWorkflow(ctx context.Context, req *workflowspb.CreateWorkflowRequest) (string, error)
	UpdateWorkflow(ctx context.Context, req *workflowspb.UpdateWorkflowRequest) error
	UpdateWorkflowBuildStatus(ctx context.Context, req *workflowspb.UpdateWorkflowBuildStatusRequest) error
	GetWorkflow(ctx context.Context, req *workflowspb.GetWorkflowRequest) (*workflowsmodel.GetWorkflowResponse, error)
	GetWorkflowByID(ctx context.Context, req *workflowspb.GetWorkflowByIDRequest) (*workflowsmodel.GetWorkflowByIDResponse, error)
	TerminateWorkflow(ctx context.Context, req *workflowspb.TerminateWorkflowRequest) error
	ListWorkflows(ctx context.Context, req *workflowspb.ListWorkflowsRequest) (*workflowsmodel.ListWorkflowsResponse, error)
}

// Config represents the workflows-service configuration.
type Config struct {
	Deadline time.Duration
}

// Jobs represents the workflows-service.
type Jobs struct {
	workflowspb.UnimplementedWorkflowsServiceServer
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
		"UpdateWorkflowBuildStatus",
		"GetWorkflowByID",
	}

	for _, api := range internalAPIs {
		if strings.Contains(fullMethod, api) {
			return true
		}
	}

	return false
}

// New creates a new workflows server.
func New(ctx context.Context, cfg *Config, auth auth.IAuth, svc Service) *grpc.Server {
	workflows := &Jobs{
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
			workflows.authTokenInterceptor(),
		),
	)
	workflowspb.RegisterWorkflowsServiceServer(server, workflows)

	reflection.Register(server)
	return server
}

// CreateWorkflow creates a new job.
func (j *Jobs) CreateWorkflow(ctx context.Context, req *workflowspb.CreateWorkflowRequest) (res *workflowspb.CreateWorkflowResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.CreateWorkflow",
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

	jobID, err := j.svc.CreateWorkflow(ctx, req)
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
	return &workflowspb.CreateWorkflowResponse{Id: jobID}, nil
}

// UpdateWorkflow updates the job details.
func (j *Jobs) UpdateWorkflow(ctx context.Context, req *workflowspb.UpdateWorkflowRequest) (res *workflowspb.UpdateWorkflowResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.UpdateWorkflow",
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

	err = j.svc.UpdateWorkflow(ctx, req)
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
	return &workflowspb.UpdateWorkflowResponse{}, nil
}

// UpdateWorkflowBuildStatus updates the job build status.
// This is an internal method used by internal services, and it should not be exposed to the public.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (j *Jobs) UpdateWorkflowBuildStatus(
	ctx context.Context,
	req *workflowspb.UpdateWorkflowBuildStatusRequest,
) (res *workflowspb.UpdateWorkflowBuildStatusResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.UpdateWorkflowBuildStatus",
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

	err = j.svc.UpdateWorkflowBuildStatus(ctx, req)
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
	return &workflowspb.UpdateWorkflowBuildStatusResponse{}, nil
}

// GetWorkflow returns the job details by ID and user ID.
func (j *Jobs) GetWorkflow(ctx context.Context, req *workflowspb.GetWorkflowRequest) (res *workflowspb.GetWorkflowResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.GetWorkflow",
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

	job, err := j.svc.GetWorkflow(ctx, req)
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

// GetWorkflowByID returns the job details by ID.
// This is an internal method used by internal services, and it should not be exposed to the public.
func (j *Jobs) GetWorkflowByID(ctx context.Context, req *workflowspb.GetWorkflowByIDRequest) (res *workflowspb.GetWorkflowByIDResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.GetWorkflowByID",
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

	job, err := j.svc.GetWorkflowByID(ctx, req)
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

// TerminateWorkflow terminates a job.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (j *Jobs) TerminateWorkflow(ctx context.Context, req *workflowspb.TerminateWorkflowRequest) (res *workflowspb.TerminateWorkflowResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.TerminateWorkflow",
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

	err = j.svc.TerminateWorkflow(ctx, req)
	if err != nil {
		j.logger.Error(
			"failed to terminate job",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"job terminated successfully",
		zap.Any("ctx", ctx),
	)
	return &workflowspb.TerminateWorkflowResponse{}, nil
}

// ListWorkflows returns the workflows by user ID.
func (j *Jobs) ListWorkflows(ctx context.Context, req *workflowspb.ListWorkflowsRequest) (res *workflowspb.ListWorkflowsResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.ListWorkflows",
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

	workflows, err := j.svc.ListWorkflows(ctx, req)
	if err != nil {
		j.logger.Error(
			"failed to fetch all workflows by user ID",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	j.logger.Info(
		"workflows details fetched successfully",
		zap.Any("ctx", ctx),
		zap.Any("workflows", workflows),
	)
	return workflows.ToProto(), nil
}
