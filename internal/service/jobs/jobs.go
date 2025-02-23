//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package jobs

import (
	"context"
	"encoding/json"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	"github.com/hitesh22rana/chronoverse/internal/model"
	"github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides job related operations.
type Repository interface {
	CreateJob(ctx context.Context, userID, name, payload, kind string, interval, maxRetries int32) (string, error)
	GetJobByID(ctx context.Context, jobID string) (*model.GetJobByIDResponse, error)
	ListJobsByUserID(ctx context.Context, userID string) (*model.ListJobsByUserIDResponse, error)
	ListScheduledJobsByJobID(ctx context.Context, jobID string) (*model.ListScheduledJobsByJobIDResponse, error)
}

// Service provides job related operations.
type Service struct {
	validator *validator.Validate
	tp        trace.Tracer
	repo      Repository
}

// New creates a new jobs-service.
func New(validator *validator.Validate, repo Repository) *Service {
	return &Service{
		validator: validator,
		tp:        otel.Tracer(svc.Info().GetName()),
		repo:      repo,
	}
}

// CreateJobRequest holds the request parameters for creating a new job.
type CreateJobRequest struct {
	UserID   string `validate:"required"`
	Name     string `validate:"required"`
	Payload  string `validate:"required"`
	Kind     string `validate:"required"`
	Interval int32  `validate:"required"`
	MaxRetry int32  `validate:"required"`
}

// CreateJob a new job.
func (s *Service) CreateJob(ctx context.Context, req *jobspb.CreateJobRequest) (jobID string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.CreateJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&CreateJobRequest{
		UserID:   req.GetUserId(),
		Name:     req.GetName(),
		Payload:  req.GetPayload(),
		Kind:     req.GetKind(),
		Interval: req.GetInterval(),
		MaxRetry: req.GetMaxRetry(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate the JSON payload
	var _payload map[string]interface{}
	if err = json.Unmarshal([]byte(req.GetPayload()), &_payload); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid payload: %v", err)
		return "", err
	}

	// CreateJob the job
	jobID, err = s.repo.CreateJob(
		ctx,
		req.GetUserId(),
		req.GetName(),
		req.GetPayload(),
		req.GetKind(),
		req.GetInterval(),
		req.GetMaxRetry(),
	)
	if err != nil {
		return "", err
	}

	return jobID, nil
}

// GetJobByIDRequest holds the request parameters for getting a job by ID.
type GetJobByIDRequest struct {
	ID string `validate:"required"`
}

// GetJobByID returns the job details by ID.
func (s *Service) GetJobByID(ctx context.Context, req *jobspb.GetJobByIDRequest) (res *model.GetJobByIDResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.GetJobByID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&GetJobByIDRequest{
		ID: req.GetId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Get the job details
	res, err = s.repo.GetJobByID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}

	return res, nil
}

// ListJobsByUserIDRequest holds the request parameters for listing jobs by user ID.
type ListJobsByUserIDRequest struct {
	UserID string `validate:"required"`
}

// ListJobsByUserID returns jobs by user ID.
func (s *Service) ListJobsByUserID(ctx context.Context, req *jobspb.ListJobsByUserIDRequest) (res *model.ListJobsByUserIDResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.ListJobsByUserID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&ListJobsByUserIDRequest{
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// List all jobs by user ID
	res, err = s.repo.ListJobsByUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}

	return res, nil
}

// ListScheduledJobsByJobIDRequest holds the request parameters for listing scheduled jobs by job ID.
type ListScheduledJobsByJobIDRequest struct {
	JobID string `validate:"required"`
}

// ListScheduledJobsByJobID returns scheduled jobs by job ID.
//
//nolint:lll // It's okay to have big function signature.
func (s *Service) ListScheduledJobsByJobID(ctx context.Context, req *jobspb.ListScheduledJobsByJobIDRequest) (res *model.ListScheduledJobsByJobIDResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.ListScheduledJobsByJobID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&ListScheduledJobsByJobIDRequest{
		JobID: req.GetJobId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// List all scheduled jobs by job ID
	res, err = s.repo.ListScheduledJobsByJobID(ctx, req.GetJobId())
	if err != nil {
		return nil, err
	}

	return res, nil
}
