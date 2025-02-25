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
	CreateJob(ctx context.Context, userID, name, payload, kind string, interval, maxRetry int32) (string, error)
	UpdateJob(ctx context.Context, jobID, userID, name, payload, kind string, interval int32) error
	GetJob(ctx context.Context, jobID, userID string) (*model.GetJobResponse, error)
	GetJobByID(ctx context.Context, jobID string) (*model.GetJobByIDResponse, error)
	ListJobsByUserID(ctx context.Context, userID string) (*model.ListJobsByUserIDResponse, error)
	ListScheduledJobs(ctx context.Context, jobID, userID string) (*model.ListScheduledJobsResponse, error)
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

// UpdateJobRequest holds the request parameters for updating a job.
type UpdateJobRequest struct {
	ID       string `validate:"required"`
	UserID   string `validate:"required"`
	Name     string `validate:"required"`
	Payload  string `validate:"required"`
	Kind     string `validate:"required"`
	Interval int32  `validate:"required"`
}

// UpdateJob updates the job details.
func (s *Service) UpdateJob(ctx context.Context, req *jobspb.UpdateJobRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.UpdateJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	if err = s.validator.Struct(&UpdateJobRequest{
		ID:       req.GetId(),
		UserID:   req.GetUserId(),
		Name:     req.GetName(),
		Payload:  req.GetPayload(),
		Kind:     req.GetKind(),
		Interval: req.GetInterval(),
	}); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Validate the JSON payload
	var _payload map[string]interface{}
	if err = json.Unmarshal([]byte(req.GetPayload()), &_payload); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid payload: %v", err)
		return err
	}

	// Update the job details
	err = s.repo.UpdateJob(
		ctx,
		req.GetId(),
		req.GetUserId(),
		req.GetName(),
		req.GetPayload(),
		req.GetKind(),
		req.GetInterval(),
	)
	return err
}

// GetJobRequest holds the request parameters for getting a job.
type GetJobRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// GetJob returns the job details by ID and user ID.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (s *Service) GetJob(ctx context.Context, req *jobspb.GetJobRequest) (res *model.GetJobResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.GetJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&GetJobRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Get the job details
	res, err = s.repo.GetJob(ctx, req.GetId(), req.GetUserId())
	if err != nil {
		return nil, err
	}

	return res, nil
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

// ListScheduledJobsRequest holds the request parameters for listing scheduled jobs by job ID.
type ListScheduledJobsRequest struct {
	JobID  string `validate:"required"`
	UserID string `validate:"required"`
}

// ListScheduledJobs returns scheduled jobs by job ID.
//
//nolint:dupl // It's okay to have similar code for different methods.
func (s *Service) ListScheduledJobs(ctx context.Context, req *jobspb.ListScheduledJobsRequest) (res *model.ListScheduledJobsResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.ListScheduledJobs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&ListScheduledJobsRequest{
		JobID:  req.GetJobId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// List all scheduled jobs by job ID
	res, err = s.repo.ListScheduledJobs(ctx, req.GetJobId(), req.GetUserId())
	if err != nil {
		return nil, err
	}

	return res, nil
}
