//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package jobs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	defaultExpirationTTL      = time.Minute * 30
	jobLogSearchExpirationTTL = time.Minute * 15
	cacheTimeout              = time.Second * 5
)

// Repository provides job related operations.
type Repository interface {
	ScheduleJob(ctx context.Context, workflowID, userID, scheduledAt, trigger string) (string, error)
	UpdateJobStatus(ctx context.Context, jobID, containerID, jobStatus string) error
	GetJob(ctx context.Context, jobID, workflowID, userID string) (*jobsmodel.GetJobResponse, error)
	GetJobByID(ctx context.Context, jobID string) (*jobsmodel.GetJobByIDResponse, error)
	GetJobLogs(ctx context.Context, jobID, workflowID, userID, cursor string, filters *jobsmodel.GetJobLogsFilters) (*jobsmodel.GetJobLogsResponse, string, error)
	StreamJobLogs(ctx context.Context, jobID, workflowID, userID string) (*goredis.PubSub, error)
	SearchJobLogs(ctx context.Context, jobID, workflowID, userID, cursor string, filters *jobsmodel.SearchJobLogsFilters) (*jobsmodel.GetJobLogsResponse, string, error)
	ListJobs(ctx context.Context, workflowID, userID, cursor string, filters *jobsmodel.ListJobsFilters) (*jobsmodel.ListJobsResponse, error)
}

// Cache provides cache related operations.
type Cache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Get(ctx context.Context, key string, dest any) (any, error)
}

// Service provides job related operations.
type Service struct {
	validator *validator.Validate
	tp        trace.Tracer
	repo      Repository
	cache     Cache
}

// New creates a new jobs-service.
func New(validator *validator.Validate, repo Repository, cache Cache) *Service {
	return &Service{
		validator: validator,
		tp:        otel.Tracer(svcpkg.Info().GetName()),
		repo:      repo,
		cache:     cache,
	}
}

// ScheduleJobRequest holds the request parameters for scheduling a job.
type ScheduleJobRequest struct {
	WorkflowID  string `validate:"required"`
	UserID      string `validate:"required"`
	ScheduledAt string `validate:"required"`
	Trigger     string `validate:"required"`
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
		Trigger:     req.GetTrigger(),
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

	// Validate the job trigger
	err = validateJobTrigger(req.GetTrigger())
	if err != nil {
		return "", err
	}

	// Schedule the job
	res, err := s.repo.ScheduleJob(
		ctx,
		req.GetWorkflowId(),
		req.GetUserId(),
		req.GetScheduledAt(),
		req.GetTrigger(),
	)
	if err != nil {
		return "", err
	}

	return res, nil
}

// UpdateJobStatusRequest holds the request parameters for updating a scheduled job status.
type UpdateJobStatusRequest struct {
	ID          string `validate:"required"`
	ContainerID string `validate:"omitempty"`
	Status      string `validate:"required"`
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
		ID:          req.GetId(),
		ContainerID: req.GetContainerId(),
		Status:      req.GetStatus(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Validate the job status
	err = validateJobStatus(req.GetStatus())
	if err != nil {
		return err
	}

	// Update the scheduled job status
	err = s.repo.UpdateJobStatus(
		ctx,
		req.GetId(),
		req.GetContainerId(),
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
	ID         string                       `validate:"required"`
	WorkflowID string                       `validate:"required"`
	UserID     string                       `validate:"required"`
	Cursor     string                       `validate:"omitempty"`
	Filters    *jobsmodel.GetJobLogsFilters `validate:"required"`
}

// GetJobLogs returns the scheduled job logs.
func (s *Service) GetJobLogs(ctx context.Context, req *jobspb.GetJobLogsRequest) (res *jobsmodel.GetJobLogsResponse, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.GetJobLogs"),
	)
	ctx, span := s.tp.Start(ctx, "Service.GetJobLogs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	var filters *jobsmodel.GetJobLogsFilters
	if req.GetFilters() != nil {
		filters = &jobsmodel.GetJobLogsFilters{
			Stream: int(req.GetFilters().GetStream()),
		}
	} else {
		filters = &jobsmodel.GetJobLogsFilters{}
	}

	// Validate the request
	err = s.validator.Struct(&GetJobLogsRequest{
		ID:         req.GetId(),
		WorkflowID: req.GetWorkflowId(),
		UserID:     req.GetUserId(),
		Cursor:     req.GetCursor(),
		Filters:    filters,
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check if the job logs are cached
	cacheKey := fmt.Sprintf(
		"job_logs:%s:%s:%s:%s",
		req.GetUserId(),
		req.GetId(),
		req.GetCursor(),
		req.GetFilters().GetStream(),
	)
	cacheRes, cacheErr := s.cache.Get(ctx, cacheKey, &jobsmodel.GetJobLogsResponse{})
	if cacheErr != nil {
		if errors.Is(cacheErr, context.DeadlineExceeded) || errors.Is(cacheErr, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, cacheErr.Error())
			return nil, err
		}
	} else {
		// Cache hit, return cached response
		//nolint:errcheck,forcetypeassert // Ignore error as we are just reading from cache
		return cacheRes.(*jobsmodel.GetJobLogsResponse), nil
	}

	// Get the scheduled job logs
	var jobStatus string
	res, jobStatus, err = s.repo.GetJobLogs(
		ctx,
		req.GetId(),
		req.GetWorkflowId(),
		req.GetUserId(),
		req.GetCursor(),
		filters,
	)
	if err != nil {
		return nil, err
	}

	// Cache the response in the background, if any of the following conditions are satisfied:
	// 1. The job status is in terminal state(which ensures the job status won't change further)
	// 2. Next cursor exists(which ensure that these are not the trailing logs and can be cached)
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	if isTerminalJobStatus(jobStatus) || res.Cursor != "" {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
			defer cancel()

			// Cache the job logs
			if setErr := s.cache.Set(bgCtx, cacheKey, res, defaultExpirationTTL); setErr != nil {
				logger.Warn("failed to cache job logs",
					zap.String("user_id", req.GetUserId()),
					zap.String("job_id", req.GetId()),
					zap.String("cache_key", cacheKey),
					zap.Error(setErr),
				)
			} else if logger.Core().Enabled(zap.DebugLevel) {
				logger.Debug("cached job logs",
					zap.String("user_id", req.GetUserId()),
					zap.String("job_id", req.GetId()),
					zap.String("cache_key", cacheKey),
				)
			}
		}()
	}

	return res, nil
}

// StreamJobLogsRequest holds the request parameters for streaming scheduled job logs.
type StreamJobLogsRequest struct {
	ID         string `validate:"required"`
	WorkflowID string `validate:"required"`
	UserID     string `validate:"required"`
}

// StreamJobLogs streams the job logs.
func (s *Service) StreamJobLogs(ctx context.Context, req *jobspb.StreamJobLogsRequest) (ch chan *jobsmodel.JobLog, err error) {
	ctx, span := s.tp.Start(ctx, "Service.StreamJobLogs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&StreamJobLogsRequest{
		ID:         req.GetId(),
		WorkflowID: req.GetWorkflowId(),
		UserID:     req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Stream the scheduled job logs
	sub, _err := s.repo.StreamJobLogs(ctx, req.GetId(), req.GetWorkflowId(), req.GetUserId())
	if _err != nil {
		err = _err
		return nil, err
	}

	subscribedChannel := sub.Channel()
	ch = make(chan *jobsmodel.JobLog)
	go func() {
		defer close(ch)
		//nolint:errcheck // Ignore error as we are closing the channel
		defer sub.Unsubscribe(ctx, redis.GetJobLogsChannel(req.GetId()))
		defer sub.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-subscribedChannel:
				if !ok {
					return
				}

				if data == nil {
					continue
				}

				payload := data.Payload
				var log jobsmodel.JobLogEvent
				if err := json.Unmarshal([]byte(payload), &log); err != nil {
					continue
				}

				select {
				case ch <- &jobsmodel.JobLog{
					Timestamp:   log.TimeStamp,
					Message:     log.Message,
					SequenceNum: log.SequenceNum,
					Stream:      log.Stream,
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

// SearchJobLogsRequest holds the request parameters for getting filtered logs of a job.
type SearchJobLogsRequest struct {
	ID         string                          `validate:"required"`
	WorkflowID string                          `validate:"required"`
	UserID     string                          `validate:"required"`
	Cursor     string                          `validate:"omitempty"`
	Filters    *jobsmodel.SearchJobLogsFilters `validate:"required"`
}

// SearchJobLogs returns the filtered logs of a job.
func (s *Service) SearchJobLogs(ctx context.Context, req *jobspb.SearchJobLogsRequest) (res *jobsmodel.GetJobLogsResponse, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.SearchJobLogs"),
	)
	ctx, span := s.tp.Start(ctx, "Service.SearchJobLogs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	var filters *jobsmodel.SearchJobLogsFilters
	if req.GetFilters() != nil {
		filters = &jobsmodel.SearchJobLogsFilters{
			Stream:  int(req.GetFilters().GetStream()),
			Message: req.GetFilters().GetMessage(),
		}
	} else {
		filters = &jobsmodel.SearchJobLogsFilters{}
	}

	// Validate the struct
	err = s.validator.Struct(&SearchJobLogsRequest{
		ID:         req.GetId(),
		WorkflowID: req.GetWorkflowId(),
		UserID:     req.GetUserId(),
		Cursor:     req.GetCursor(),
		Filters:    filters,
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check if the job logs are cached
	cacheKey := fmt.Sprintf(
		"job_logs:%s:%s:%s:%s:%s",
		req.GetUserId(),
		req.GetId(),
		req.GetCursor(),
		req.GetFilters().GetMessage(),
		req.GetFilters().GetStream(),
	)
	cacheRes, cacheErr := s.cache.Get(ctx, cacheKey, &jobsmodel.GetJobLogsResponse{})
	if cacheErr != nil {
		if errors.Is(cacheErr, context.DeadlineExceeded) || errors.Is(cacheErr, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, cacheErr.Error())
			return nil, err
		}
	} else {
		// Cache hit, return cached response
		//nolint:errcheck,forcetypeassert // Ignore error as we are just reading from cache
		return cacheRes.(*jobsmodel.GetJobLogsResponse), nil
	}

	// Get all the filtered job logs
	var jobStatus string
	res, jobStatus, err = s.repo.SearchJobLogs(
		ctx,
		req.GetId(),
		req.GetWorkflowId(),
		req.GetUserId(),
		req.GetCursor(),
		filters,
	)
	if err != nil {
		return nil, err
	}

	// Cache the response in the background, if any of the following conditions are satisfied:
	// 1. The job status is in terminal state(which ensures the job status won't change further)
	// 2. Next cursor exists(which ensure that these are not the trailing logs and can be cached)
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	if isTerminalJobStatus(jobStatus) || res.Cursor != "" {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
			defer cancel()

			// Cache the job logs
			if setErr := s.cache.Set(bgCtx, cacheKey, res, jobLogSearchExpirationTTL); setErr != nil {
				logger.Warn("failed to cache job logs",
					zap.String("user_id", req.GetUserId()),
					zap.String("job_id", req.GetId()),
					zap.String("cache_key", cacheKey),
					zap.Error(setErr),
				)
			} else if logger.Core().Enabled(zap.DebugLevel) {
				logger.Debug("cached job logs",
					zap.String("user_id", req.GetUserId()),
					zap.String("job_id", req.GetId()),
					zap.String("cache_key", cacheKey),
				)
			}
		}()
	}

	return res, nil
}

// ListJobsRequest holds the request parameters for listing scheduled jobs.
type ListJobsRequest struct {
	WorkflowID string                     `validate:"required"`
	UserID     string                     `validate:"required"`
	Cursor     string                     `validate:"omitempty"`
	Filters    *jobsmodel.ListJobsFilters `validate:"omitempty"`
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

	var filters *jobsmodel.ListJobsFilters
	if req.GetFilters() != nil {
		filters = &jobsmodel.ListJobsFilters{
			Status:  req.GetFilters().GetStatus(),
			Trigger: req.GetFilters().GetTrigger(),
		}
	} else {
		filters = &jobsmodel.ListJobsFilters{}
	}

	// Validate the request
	err = s.validator.Struct(&ListJobsRequest{
		WorkflowID: req.GetWorkflowId(),
		UserID:     req.GetUserId(),
		Cursor:     req.GetCursor(),
		Filters:    filters,
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate the cursor
	var cursor string
	if req.GetCursor() != "" {
		cursor, err = decodeListJobsCursor(req.GetCursor())
		if err != nil {
			err = status.Errorf(codes.InvalidArgument, "invalid cursor: %v", err)
			return nil, err
		}
	}

	// Validate the filters
	if err = validateListJobsFilters(filters); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid filters: %v", err)
		return nil, err
	}

	// List all scheduled jobs by job ID
	res, err = s.repo.ListJobs(ctx, req.GetWorkflowId(), req.GetUserId(), cursor, filters)
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
		jobsmodel.JobStatusFailed.ToString(),
		jobsmodel.JobStatusCanceled.ToString():
		return nil
	default:
		return status.Errorf(codes.InvalidArgument, "invalid status: %s", s)
	}
}

func validateJobTrigger(t string) error {
	switch t {
	case jobsmodel.JobTriggerAutomatic.ToString(),
		jobsmodel.JobTriggerManual.ToString():
		return nil
	default:
		return status.Errorf(codes.InvalidArgument, "invalid trigger: %s", t)
	}
}

func isTerminalJobStatus(jobStatus string) bool {
	switch jobStatus {
	case jobsmodel.JobStatusCompleted.ToString(),
		jobsmodel.JobStatusCanceled.ToString(),
		jobsmodel.JobStatusFailed.ToString():
		return true
	default:
		return false
	}
}

func validateListJobsFilters(filters *jobsmodel.ListJobsFilters) error {
	if filters == nil {
		return nil
	}

	if filters.Status != "" {
		err := validateJobStatus(filters.Status)
		if err != nil {
			return err
		}
	}

	return nil
}

func decodeListJobsCursor(token string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}

	return string(decoded), nil
}
