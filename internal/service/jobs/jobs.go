//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package jobs

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides job related operations.
type Repository interface {
	ScheduleJob(ctx context.Context, workflowID, userID, scheduledAt string) (string, error)
	UpdateJobStatus(ctx context.Context, jobID, jobStatus string) error
	GetJob(ctx context.Context, jobID, workflowID, userID string) (*jobsmodel.GetJobResponse, error)
	GetJobByID(ctx context.Context, jobID string) (*jobsmodel.GetJobByIDResponse, error)
	GetJobLogs(ctx context.Context, jobID, workflowID, userID, cursor string) (*jobsmodel.GetJobLogsResponse, error)
	ListJobs(ctx context.Context, workflowID, userID, cursor string) (*jobsmodel.ListJobsResponse, error)
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
		tp:        otel.Tracer(svcpkg.Info().GetName()),
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
}

// ScheduleJobRequest holds the request parameters for scheduling a job.
type ScheduleJobRequest struct {
	WorkflowID  string `validate:"required"`
	UserID      string `validate:"required"`
	ScheduledAt string `validate:"required"`
}

// ScheduleJob schedules a job.
func (s *Service) ScheduleJob(ctx context.Context, req *jobspb.ScheduleJobRequest) (jobID string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.ScheduleJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&ScheduleJobRequest{
		WorkflowID:  req.GetWorkflowId(),
		UserID:      req.GetUserId(),
		ScheduledAt: req.GetScheduledAt(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate the scheduled time
	err = validateTime(req.GetScheduledAt())
	if err != nil {
		return "", err
	}

	// Schedule the job
	res, err := s.repo.ScheduleJob(
		ctx,
		req.GetWorkflowId(),
		req.GetUserId(),
		req.GetScheduledAt(),
	)
	if err != nil {
		return "", err
	}

	return res, nil
}

// UpdateJobStatusRequest holds the request parameters for updating a scheduled job status.
type UpdateJobStatusRequest struct {
	ID     string `validate:"required"`
	Status string `validate:"required"`
}

// UpdateJobStatus updates the scheduled job status.
func (s *Service) UpdateJobStatus(ctx context.Context, req *jobspb.UpdateJobStatusRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.UpdateJobStatus")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&UpdateJobStatusRequest{
		ID:     req.GetId(),
		Status: req.GetStatus(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Validate the status
	err = validateJobStatus(req.GetStatus())
	if err != nil {
		return err
	}

	// Update the scheduled job status
	err = s.repo.UpdateJobStatus(
		ctx,
		req.GetId(),
		req.GetStatus(),
	)

	return err
}

// GetJobRequest holds the request parameters for getting a scheduled job.
type GetJobRequest struct {
	ID         string `validate:"required"`
	WorkflowID string `validate:"required"`
	UserID     string `validate:"required"`
}

// GetJob returns the scheduled job details by ID, job ID, and user ID.
func (s *Service) GetJob(ctx context.Context, req *jobspb.GetJobRequest) (res *jobsmodel.GetJobResponse, err error) {
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
		ID:         req.GetId(),
		WorkflowID: req.GetWorkflowId(),
		UserID:     req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Get the scheduled job details
	res, err = s.repo.GetJob(ctx, req.GetId(), req.GetWorkflowId(), req.GetUserId())
	if err != nil {
		return nil, err
	}

	return res, nil
}

// GetJobByIDRequest holds the request parameters for getting a scheduled job by ID.
type GetJobByIDRequest struct {
	ID string `validate:"required"`
}

// GetJobByID returns the scheduled job details by ID.
func (s *Service) GetJobByID(ctx context.Context, req *jobspb.GetJobByIDRequest) (res *jobsmodel.GetJobByIDResponse, err error) {
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

	// Get the scheduled job details
	res, err = s.repo.GetJobByID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}

	return res, nil
}

// GetJobLogsRequest holds the request parameters for getting scheduled job logs.
type GetJobLogsRequest struct {
	ID         string `validate:"required"`
	WorkflowID string `validate:"required"`
	UserID     string `validate:"required"`
	Cursor     string `validate:"omitempty"`
}

// GetJobLogs returns the scheduled job logs.
func (s *Service) GetJobLogs(ctx context.Context, req *jobspb.GetJobLogsRequest) (res *jobsmodel.GetJobLogsResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.GetJobLogs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&GetJobLogsRequest{
		ID:         req.GetId(),
		WorkflowID: req.GetWorkflowId(),
		UserID:     req.GetUserId(),
		Cursor:     req.GetCursor(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate the next page token
	var cursor string
	if req.GetCursor() != "" {
		cursor, err = decodeCursor(req.GetCursor())
		if err != nil {
			err = status.Errorf(codes.InvalidArgument, "invalid next page token: %v", err)
			return nil, err
		}
	}

	// Get the scheduled job logs
	res, err = s.repo.GetJobLogs(ctx, req.GetId(), req.GetWorkflowId(), req.GetUserId(), cursor)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// ListJobsRequest holds the request parameters for listing scheduled jobs.
type ListJobsRequest struct {
	WorkflowID string `validate:"required"`
	UserID     string `validate:"required"`
	Cursor     string `validate:"omitempty"`
}

// ListJobs returns scheduled jobs.
func (s *Service) ListJobs(ctx context.Context, req *jobspb.ListJobsRequest) (res *jobsmodel.ListJobsResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.ListJobs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&ListJobsRequest{
		WorkflowID: req.GetWorkflowId(),
		UserID:     req.GetUserId(),
		Cursor:     req.GetCursor(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate the next page token
	var cursor string
	if req.GetCursor() != "" {
		cursor, err = decodeCursor(req.GetCursor())
		if err != nil {
			err = status.Errorf(codes.InvalidArgument, "invalid next page token: %v", err)
			return nil, err
		}
	}

	// List all scheduled jobs by job ID
	res, err = s.repo.ListJobs(ctx, req.GetWorkflowId(), req.GetUserId(), cursor)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func validateTime(t string) error {
	_, err := time.Parse(time.RFC3339Nano, t)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid time: %v", err)
	}

	return nil
}

func validateJobStatus(s string) error {
	switch s {
	case jobsmodel.JobStatusPending.ToString(),
		jobsmodel.JobStatusQueued.ToString(),
		jobsmodel.JobStatusRunning.ToString(),
		jobsmodel.JobStatusCompleted.ToString(),
		jobsmodel.JobStatusFailed.ToString():
		return nil
	default:
		return status.Errorf(codes.InvalidArgument, "invalid status: %s", s)
	}
}

func decodeCursor(token string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}

	return string(decoded), nil
}
