//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package jobs

import (
	"context"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Service provides job related operations.
type Service interface {
	Create(ctx context.Context, userID, name, payload, kind string, interval, maxRetry int32) (string, error)
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
	cfg    *Config
	svc    Service
}

// audienceInterceptor sets the audience in the context.
func audienceInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract the audience from metadata.
		audience, err := auth.ExtractAudienceFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithAudience(ctx, audience), req)
	}
}

// roleInterceptor sets the role in the context.
func roleInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract the role from metadata.
		role, err := auth.ExtractRoleFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithRole(ctx, role), req)
	}
}

// tokenInterceptor sets the token in the context.
func tokenInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract the token from metadata.
		token, err := auth.ExtractAuthorizationTokenFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithAuthorizationToken(ctx, token), req)
	}
}

// New creates a new jobs server.
func New(ctx context.Context, cfg *Config, svc Service) *grpc.Server {
	server := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			audienceInterceptor(),
			tokenInterceptor(),
			roleInterceptor(),
		),
	)
	jobspb.RegisterJobsServiceServer(server, &Jobs{
		logger: loggerpkg.FromContext(ctx),
		tp:     otel.Tracer(svcpkg.Info().GetName()),
		cfg:    cfg,
		svc:    svc,
	})

	reflection.Register(server)
	return server
}

// Create creates a new job.
func (j *Jobs) Create(ctx context.Context, req *jobspb.CreateRequest) (res *jobspb.CreateResponse, err error) {
	ctx, span := j.tp.Start(
		ctx,
		"App.Create",
		trace.WithAttributes(
			attribute.String("user_id", req.GetUserId()),
			attribute.String("name", req.GetName()),
			attribute.String("payload", req.GetPayload()),
			attribute.String("kind", req.GetKind()),
			attribute.Int("interval", int(req.GetInterval())),
			attribute.Int("max_retry", int(req.GetMaxRetry())),
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

	jobID, err := j.svc.Create(ctx, req.GetUserId(), req.GetName(), req.GetPayload(), req.GetKind(), req.GetInterval(), req.GetMaxRetry())
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
	return &jobspb.CreateResponse{JobId: jobID}, nil
}
