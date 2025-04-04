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
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	authpkg "github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	grpcmiddlewares "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/middlewares"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// internalAPIs contains the list of internal APIs that require admin role.
// These APIs are not exposed to the public and should only be used internally.
var internalAPIs = map[string]bool{
	"UpdateWorkflowBuildStatus":                    true,
	"GetWorkflowByID":                              true,
	"IncrementWorkflowConsecutiveJobFailuresCount": true,
	"ResetWorkflowConsecutiveJobFailuresCount":     true,
}

// Service provides workflow related operations.
type Service interface {
	CreateWorkflow(ctx context.Context, req *workflowspb.CreateWorkflowRequest) (string, error)
	UpdateWorkflow(ctx context.Context, req *workflowspb.UpdateWorkflowRequest) error
	UpdateWorkflowBuildStatus(ctx context.Context, req *workflowspb.UpdateWorkflowBuildStatusRequest) error
	GetWorkflow(ctx context.Context, req *workflowspb.GetWorkflowRequest) (*workflowsmodel.GetWorkflowResponse, error)
	GetWorkflowByID(ctx context.Context, req *workflowspb.GetWorkflowByIDRequest) (*workflowsmodel.GetWorkflowByIDResponse, error)
	IncrementWorkflowConsecutiveJobFailuresCount(ctx context.Context, req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) (bool, error)
	ResetWorkflowConsecutiveJobFailuresCount(ctx context.Context, req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) error
	TerminateWorkflow(ctx context.Context, req *workflowspb.TerminateWorkflowRequest) error
	ListWorkflows(ctx context.Context, req *workflowspb.ListWorkflowsRequest) (*workflowsmodel.ListWorkflowsResponse, error)
}

// Config represents the workflows-service configuration.
type Config struct {
	Deadline    time.Duration
	Environment string
}

// Workflows represents the workflows-service.
type Workflows struct {
	workflowspb.UnimplementedWorkflowsServiceServer
	tp   trace.Tracer
	auth authpkg.IAuth
	cfg  *Config
	svc  Service
}

// authTokenInterceptor extracts and validates the authToken from the metadata and adds it to the context.
func (w *Workflows) authTokenInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the authToken from metadata.
		authToken, err := authpkg.ExtractAuthorizationTokenFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		ctx = authpkg.WithAuthorizationToken(ctx, authToken)
		if _, err := w.auth.ValidateToken(ctx); err != nil {
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

// New creates a new workflows server.
func New(ctx context.Context, cfg *Config, auth authpkg.IAuth, svc Service) *grpc.Server {
	workflows := &Workflows{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: auth,
		cfg:  cfg,
		svc:  svc,
	}

	server := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			grpcmiddlewares.LoggingInterceptor(loggerpkg.FromContext(ctx)),
			grpcmiddlewares.AudienceInterceptor(),
			grpcmiddlewares.RoleInterceptor(func(method, role string) bool {
				return isInternalAPI(method) && role != authpkg.RoleAdmin.String()
			}),
			workflows.authTokenInterceptor(),
		),
	)
	workflowspb.RegisterWorkflowsServiceServer(server, workflows)

	// Only register reflection for non-production environments.
	if !isProduction(cfg.Environment) {
		reflection.Register(server)
	}
	return server
}

// CreateWorkflow creates a new job.
func (w *Workflows) CreateWorkflow(ctx context.Context, req *workflowspb.CreateWorkflowRequest) (res *workflowspb.CreateWorkflowResponse, err error) {
	ctx, span := w.tp.Start(
		ctx,
		"App.CreateWorkflow",
		trace.WithAttributes(
			attribute.String("user_id", req.GetUserId()),
			attribute.String("name", req.GetName()),
			attribute.String("payload", req.GetPayload()),
			attribute.String("kind", req.GetKind()),
			attribute.Int("interval", int(req.GetInterval())),
			attribute.Int("max_consecutive_job_failures_allowed", int(req.GetMaxConsecutiveJobFailuresAllowed())),
		),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, w.cfg.Deadline)
	defer cancel()

	jobID, err := w.svc.CreateWorkflow(ctx, req)
	if err != nil {
		return nil, err
	}

	return &workflowspb.CreateWorkflowResponse{Id: jobID}, nil
}

// UpdateWorkflow updates the job details.
func (w *Workflows) UpdateWorkflow(ctx context.Context, req *workflowspb.UpdateWorkflowRequest) (res *workflowspb.UpdateWorkflowResponse, err error) {
	ctx, span := w.tp.Start(
		ctx,
		"App.UpdateWorkflow",
		trace.WithAttributes(
			attribute.String("id", req.GetId()),
			attribute.String("user_id", req.GetUserId()),
			attribute.String("name", req.GetName()),
			attribute.String("payload", req.GetPayload()),
			attribute.Int("interval", int(req.GetInterval())),
			attribute.Int("max_consecutive_job_failures_allowed", int(req.GetMaxConsecutiveJobFailuresAllowed())),
		),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, w.cfg.Deadline)
	defer cancel()

	err = w.svc.UpdateWorkflow(ctx, req)
	if err != nil {
		return nil, err
	}

	return &workflowspb.UpdateWorkflowResponse{}, nil
}

// UpdateWorkflowBuildStatus updates the job build status.
// This is an internal method used by internal services, and it should not be exposed to the public.
func (w *Workflows) UpdateWorkflowBuildStatus(
	ctx context.Context,
	req *workflowspb.UpdateWorkflowBuildStatusRequest,
) (res *workflowspb.UpdateWorkflowBuildStatusResponse, err error) {
	ctx, span := w.tp.Start(
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

	ctx, cancel := context.WithTimeout(ctx, w.cfg.Deadline)
	defer cancel()

	err = w.svc.UpdateWorkflowBuildStatus(ctx, req)
	if err != nil {
		return nil, err
	}

	return &workflowspb.UpdateWorkflowBuildStatusResponse{}, nil
}

// GetWorkflow returns the job details by ID and user ID.
func (w *Workflows) GetWorkflow(ctx context.Context, req *workflowspb.GetWorkflowRequest) (res *workflowspb.GetWorkflowResponse, err error) {
	ctx, span := w.tp.Start(
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

	ctx, cancel := context.WithTimeout(ctx, w.cfg.Deadline)
	defer cancel()

	job, err := w.svc.GetWorkflow(ctx, req)
	if err != nil {
		return nil, err
	}

	return job.ToProto(), nil
}

// GetWorkflowByID returns the job details by ID.
// This is an internal method used by internal services, and it should not be exposed to the public.
func (w *Workflows) GetWorkflowByID(ctx context.Context, req *workflowspb.GetWorkflowByIDRequest) (res *workflowspb.GetWorkflowByIDResponse, err error) {
	ctx, span := w.tp.Start(
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

	ctx, cancel := context.WithTimeout(ctx, w.cfg.Deadline)
	defer cancel()

	job, err := w.svc.GetWorkflowByID(ctx, req)
	if err != nil {
		return nil, err
	}

	return job.ToProto(), nil
}

// IncrementWorkflowConsecutiveJobFailuresCount increments the consecutive job failures count.
// This is an internal method used by internal services, and it should not be exposed to the public.
func (w *Workflows) IncrementWorkflowConsecutiveJobFailuresCount(
	ctx context.Context,
	req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest,
) (res *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse, err error) {
	ctx, span := w.tp.Start(
		ctx,
		"App.IncrementWorkflowConsecutiveJobFailuresCount",
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

	ctx, cancel := context.WithTimeout(ctx, w.cfg.Deadline)
	defer cancel()

	thresholdReached, err := w.svc.IncrementWorkflowConsecutiveJobFailuresCount(ctx, req)
	if err != nil {
		return nil, err
	}

	return &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse{
		ThresholdReached: thresholdReached,
	}, nil
}

// ResetWorkflowConsecutiveJobFailuresCount resets the consecutive job failures count.
// This is an internal method used by internal services, and it should not be exposed to the public.
func (w *Workflows) ResetWorkflowConsecutiveJobFailuresCount(
	ctx context.Context,
	req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest,
) (res *workflowspb.ResetWorkflowConsecutiveJobFailuresCountResponse, err error) {
	ctx, span := w.tp.Start(
		ctx,
		"App.ResetWorkflowConsecutiveJobFailuresCount",
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

	ctx, cancel := context.WithTimeout(ctx, w.cfg.Deadline)
	defer cancel()

	err = w.svc.ResetWorkflowConsecutiveJobFailuresCount(ctx, req)
	if err != nil {
		return nil, err
	}

	return &workflowspb.ResetWorkflowConsecutiveJobFailuresCountResponse{}, nil
}

// TerminateWorkflow terminates a job.
func (w *Workflows) TerminateWorkflow(ctx context.Context, req *workflowspb.TerminateWorkflowRequest) (res *workflowspb.TerminateWorkflowResponse, err error) {
	ctx, span := w.tp.Start(
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

	ctx, cancel := context.WithTimeout(ctx, w.cfg.Deadline)
	defer cancel()

	err = w.svc.TerminateWorkflow(ctx, req)
	if err != nil {
		return nil, err
	}

	return &workflowspb.TerminateWorkflowResponse{}, nil
}

// ListWorkflows returns the workflows by user ID.
func (w *Workflows) ListWorkflows(ctx context.Context, req *workflowspb.ListWorkflowsRequest) (res *workflowspb.ListWorkflowsResponse, err error) {
	ctx, span := w.tp.Start(
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

	ctx, cancel := context.WithTimeout(ctx, w.cfg.Deadline)
	defer cancel()

	workflows, err := w.svc.ListWorkflows(ctx, req)
	if err != nil {
		return nil, err
	}

	return workflows.ToProto(), nil
}
