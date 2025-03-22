//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package jobs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	"github.com/hitesh22rana/chronoverse/internal/model"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides job related operations.
//
//nolint:interfacebloat // It's okay to have all these methods in the interface.
type Repository interface {
	CreateJob(ctx context.Context, userID, name, payload, kind string, interval int32) (string, error)
	UpdateJob(ctx context.Context, jobID, userID, name, payload string, interval int32) error
	UpdateJobBuildStatus(ctx context.Context, jobID, buildStatus string) error
	GetJob(ctx context.Context, jobID, userID string) (*model.GetJobResponse, error)
	GetJobByID(ctx context.Context, jobID string) (*model.GetJobByIDResponse, error)
	TerminateJob(ctx context.Context, jobID, userID string) error
	ScheduleJob(ctx context.Context, jobID, userID, scheduledAt string) (string, error)
	UpdateScheduledJobStatus(ctx context.Context, scheduledJobID, scheduledJobStatus string) error
	GetScheduledJob(ctx context.Context, scheduledJobID, jobID, userID string) (*model.GetScheduledJobResponse, error)
	GetScheduledJobByID(ctx context.Context, scheduledJobID string) (*model.GetScheduledJobByIDResponse, error)
	ListJobsByUserID(ctx context.Context, userID, cursor string) (*model.ListJobsByUserIDResponse, error)
	ListScheduledJobs(ctx context.Context, scheduledJobID, userID, cursor string) (*model.ListScheduledJobsResponse, error)
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
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate the kind
	err = validateKind(req.GetKind())
	if err != nil {
		return "", err
	}

	// Validate the JSON payload
	var _payload map[string]any
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
		Interval: req.GetInterval(),
	}); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Validate the JSON payload
	var _payload map[string]any
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
		req.GetInterval(),
	)

	return err
}

// UpdateJobBuildStatusRequest holds the request parameters for updating a job build status.
type UpdateJobBuildStatusRequest struct {
	ID          string `validate:"required"`
	BuildStatus string `validate:"required"`
}

// UpdateJobBuildStatus updates the job build status.
func (s *Service) UpdateJobBuildStatus(ctx context.Context, req *jobspb.UpdateJobBuildStatusRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.UpdateJobBuildStatus")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&UpdateJobBuildStatusRequest{
		ID:          req.GetId(),
		BuildStatus: req.GetBuildStatus(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Validate the job build status
	err = validateJobBuildStatus(req.GetBuildStatus())
	if err != nil {
		return err
	}

	// Update the job build status
	err = s.repo.UpdateJobBuildStatus(
		ctx,
		req.GetId(),
		req.GetBuildStatus(),
	)

	return err
}

// GetJobRequest holds the request parameters for getting a job.
type GetJobRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// GetJob returns the job details by ID and user ID.
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

// ScheduleJobRequest holds the request parameters for scheduling a job.
type ScheduleJobRequest struct {
	JobID       string `validate:"required"`
	UserID      string `validate:"required"`
	ScheduledAt string `validate:"required"`
}

// TerminateJobRequest holds the request parameters for terminating a job.
type TerminateJobRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// TerminateJob terminates a job.
func (s *Service) TerminateJob(ctx context.Context, req *jobspb.TerminateJobRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.TerminateJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&TerminateJobRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Terminate the job
	err = s.repo.TerminateJob(ctx, req.GetId(), req.GetUserId())

	return err
}

// ScheduleJob schedules a job.
func (s *Service) ScheduleJob(ctx context.Context, req *jobspb.ScheduleJobRequest) (scheduledJobID string, err error) {
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
		JobID:       req.GetJobId(),
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
		req.GetJobId(),
		req.GetUserId(),
		req.GetScheduledAt(),
	)
	if err != nil {
		return "", err
	}

	return res, nil
}

// UpdateScheduledJobStatusRequest holds the request parameters for updating a scheduled job status.
type UpdateScheduledJobStatusRequest struct {
	ID     string `validate:"required"`
	Status string `validate:"required"`
}

// UpdateScheduledJobStatus updates the scheduled job status.
func (s *Service) UpdateScheduledJobStatus(ctx context.Context, req *jobspb.UpdateScheduledJobStatusRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.UpdateScheduledJobStatus")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&UpdateScheduledJobStatusRequest{
		ID:     req.GetId(),
		Status: req.GetStatus(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Validate the status
	err = validateScheduledJobStatus(req.GetStatus())
	if err != nil {
		return err
	}

	// Update the scheduled job status
	err = s.repo.UpdateScheduledJobStatus(
		ctx,
		req.GetId(),
		req.GetStatus(),
	)

	return err
}

// GetScheduledJobRequest holds the request parameters for getting a scheduled job.
type GetScheduledJobRequest struct {
	ID     string `validate:"required"`
	JobID  string `validate:"required"`
	UserID string `validate:"required"`
}

// GetScheduledJob returns the scheduled job details by ID, job ID, and user ID.
func (s *Service) GetScheduledJob(ctx context.Context, req *jobspb.GetScheduledJobRequest) (res *model.GetScheduledJobResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.GetScheduledJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&GetScheduledJobRequest{
		ID:     req.GetId(),
		JobID:  req.GetJobId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Get the scheduled job details
	res, err = s.repo.GetScheduledJob(ctx, req.GetId(), req.GetJobId(), req.GetUserId())
	if err != nil {
		return nil, err
	}

	return res, nil
}

// GetScheduledJobByIDRequest holds the request parameters for getting a scheduled job by ID.
type GetScheduledJobByIDRequest struct {
	ID string `validate:"required"`
}

// GetScheduledJobByID returns the scheduled job details by ID.
func (s *Service) GetScheduledJobByID(ctx context.Context, req *jobspb.GetScheduledJobByIDRequest) (res *model.GetScheduledJobByIDResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.GetScheduledJobByID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&GetScheduledJobByIDRequest{
		ID: req.GetId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Get the scheduled job details
	res, err = s.repo.GetScheduledJobByID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}

	return res, nil
}

// ListJobsByUserIDRequest holds the request parameters for listing jobs by user ID.
type ListJobsByUserIDRequest struct {
	UserID string `validate:"required"`
	Cursor string `validate:"omitempty"`
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

	// Validate the next page token
	var cursor string
	if req.GetCursor() != "" {
		cursor, err = decodeCursor(req.GetCursor())
		if err != nil {
			err = status.Errorf(codes.InvalidArgument, "invalid next page token: %v", err)
			return nil, err
		}
	}

	// List all jobs by user ID
	res, err = s.repo.ListJobsByUserID(ctx, req.GetUserId(), cursor)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// ListScheduledJobsRequest holds the request parameters for listing scheduled jobs.
type ListScheduledJobsRequest struct {
	JobID  string `validate:"required"`
	UserID string `validate:"required"`
	Cursor string `validate:"omitempty"`
}

// ListScheduledJobs returns scheduled jobs.
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
		Cursor: req.GetCursor(),
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
	res, err = s.repo.ListScheduledJobs(ctx, req.GetJobId(), req.GetUserId(), cursor)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func validateJobBuildStatus(s string) error {
	switch s {
	case model.JobBuildStatusQueued.ToString(),
		model.JobBuildStatusStarted.ToString(),
		model.JobBuildStatusCompleted.ToString(),
		model.JobBuildStatusFailed.ToString(),
		model.JobBuildStatusCanceled.ToString():
		return nil
	default:
		return status.Errorf(codes.InvalidArgument, "invalid build status: %s", s)
	}
}

func validateKind(k string) error {
	switch k {
	case model.KindHeartbeat.ToString(),
		model.KindContainer.ToString():
		return nil
	default:
		return status.Errorf(codes.InvalidArgument, "invalid kind: %s", k)
	}
}

func validateTime(t string) error {
	_, err := time.Parse(time.RFC3339Nano, t)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid time: %v", err)
	}

	return nil
}

func validateScheduledJobStatus(s string) error {
	switch s {
	case model.ScheduledJobStatusPending.ToString(),
		model.ScheduledJobStatusQueued.ToString(),
		model.ScheduledJobStatusRunning.ToString(),
		model.ScheduledJobStatusCompleted.ToString(),
		model.ScheduledJobStatusFailed.ToString():
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
