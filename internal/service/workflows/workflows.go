//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package workflows

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/heartbeat"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	defaultExpirationTTL = time.Minute * 30
	cacheTimeout         = time.Second * 5
)

// Repository provides job related operations.
type Repository interface {
	CreateWorkflow(ctx context.Context, userID, name, payload, kind string, interval, maxConsecutiveJobFailuresAllowed int32) (*workflowsmodel.GetWorkflowResponse, error)
	UpdateWorkflow(ctx context.Context, workflowID, userID, name, payload string, interval, maxConsecutiveJobFailuresAllowed int32) error
	UpdateWorkflowBuildStatus(ctx context.Context, workflowID, userID, buildStatus string) error
	GetWorkflow(ctx context.Context, workflowID, userID string) (*workflowsmodel.GetWorkflowResponse, error)
	GetWorkflowByID(ctx context.Context, workflowID string) (*workflowsmodel.GetWorkflowByIDResponse, error)
	IncrementWorkflowConsecutiveJobFailuresCount(ctx context.Context, workflowID, userID string) (bool, error)
	ResetWorkflowConsecutiveJobFailuresCount(ctx context.Context, workflowID, userID string) error
	TerminateWorkflow(ctx context.Context, workflowID, userID string) error
	DeleteWorkflow(ctx context.Context, workflowID, userID string) error
	ListWorkflows(ctx context.Context, userID, cursor string, filters *workflowsmodel.ListWorkflowsFilters) (*workflowsmodel.ListWorkflowsResponse, error)
}

// Cache provides cache related operations.
type Cache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Get(ctx context.Context, key string, dest any) (any, error)
	Delete(ctx context.Context, key string) error
	DeleteByPattern(ctx context.Context, pattern string) (int64, error)
}

// Service provides job related operations.
type Service struct {
	validator *validator.Validate
	tp        trace.Tracer
	repo      Repository
	cache     Cache
}

// New creates a new workflows-service.
func New(validator *validator.Validate, repo Repository, cache Cache) *Service {
	return &Service{
		validator: validator,
		tp:        otel.Tracer(svcpkg.Info().GetName()),
		repo:      repo,
		cache:     cache,
	}
}

// CreateWorkflowRequest holds the request parameters for creating a new job.
type CreateWorkflowRequest struct {
	UserID                           string `validate:"required"`
	Name                             string `validate:"required"`
	Payload                          string `validate:"required"`
	Kind                             string `validate:"required"`
	Interval                         int32  `validate:"required,min=1,max=10080"` // Interval in minutes, max 1 week (10080 minutes)
	MaxConsecutiveJobFailuresAllowed int32  `validate:"required,min=3,max=100"`   // Max consecutive job failures allowed
}

// CreateWorkflow a new job.
func (s *Service) CreateWorkflow(ctx context.Context, req *workflowspb.CreateWorkflowRequest) (jobID string, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.CreateWorkflow"),
	)
	ctx, span := s.tp.Start(ctx, "Service.CreateWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&CreateWorkflowRequest{
		UserID:                           req.GetUserId(),
		Name:                             req.GetName(),
		Payload:                          req.GetPayload(),
		Kind:                             req.GetKind(),
		Interval:                         req.GetInterval(),
		MaxConsecutiveJobFailuresAllowed: req.GetMaxConsecutiveJobFailuresAllowed(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", status.Error(codes.InvalidArgument, err.Error())
	}

	// Validation based on kind
	switch req.GetKind() {
	case workflowsmodel.KindHeartbeat.ToString():
		_, err = heartbeat.ExtractAndValidateHeartbeatDetails(req.GetPayload())
		if err != nil {
			return "", err
		}
	case workflowsmodel.KindContainer.ToString():
		_, err = container.ExtractAndValidateContainerDetails(req.GetPayload())
		if err != nil {
			return "", err
		}
	default:
		return "", status.Errorf(codes.InvalidArgument, "invalid kind: %s", req.GetKind())
	}

	// CreateWorkflow the job
	res, err := s.repo.CreateWorkflow(
		ctx,
		req.GetUserId(),
		req.GetName(),
		req.GetPayload(),
		req.GetKind(),
		req.GetInterval(),
		req.GetMaxConsecutiveJobFailuresAllowed(),
	)
	if err != nil {
		return "", err
	}

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
		defer cancel()

		// Invalidate all list entries for the user
		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)

		// We use the combination of user ID and job ID as the key to uniquely identify the workflow.
		// The key is in the format "workflow:{user_id}:{job_id}".
		cacheKey := fmt.Sprintf("workflow:%s:%s", req.GetUserId(), res.ID)

		if setErr := s.cache.Set(bgCtx, cacheKey, res, defaultExpirationTTL); setErr != nil {
			logger.Warn("failed to cache workflow",
				zap.String("user_id", req.GetUserId()),
				zap.String("cache_key", cacheKey),
				zap.Error(setErr),
			)
		} else if logger.Core().Enabled(zap.DebugLevel) {
			logger.Debug("cached workflow",
				zap.String("user_id", req.GetUserId()),
				zap.String("cache_key", cacheKey),
			)
		}
	}()

	return res.ID, nil
}

// UpdateWorkflowRequest holds the request parameters for updating a job.
type UpdateWorkflowRequest struct {
	ID                               string `validate:"required"`
	UserID                           string `validate:"required"`
	Name                             string `validate:"required"`
	Payload                          string `validate:"required"`
	Interval                         int32  `validate:"required,min=1,max=10080"` // Interval in minutes, max 1 week (10080 minutes)
	MaxConsecutiveJobFailuresAllowed int32  `validate:"required,min=3,max=100"`   // Max consecutive job failures allowed
}

// UpdateWorkflow updates the job details.
func (s *Service) UpdateWorkflow(ctx context.Context, req *workflowspb.UpdateWorkflowRequest) (err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.UpdateWorkflow"),
	)
	ctx, span := s.tp.Start(ctx, "Service.UpdateWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	if err = s.validator.Struct(&UpdateWorkflowRequest{
		ID:                               req.GetId(),
		UserID:                           req.GetUserId(),
		Name:                             req.GetName(),
		Payload:                          req.GetPayload(),
		Interval:                         req.GetInterval(),
		MaxConsecutiveJobFailuresAllowed: req.GetMaxConsecutiveJobFailuresAllowed(),
	}); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Validate payload
	_, heartBeatPayloadErr := heartbeat.ExtractAndValidateHeartbeatDetails(req.GetPayload())
	_, containerPayloadErr := container.ExtractAndValidateContainerDetails(req.GetPayload())

	if heartBeatPayloadErr != nil && containerPayloadErr != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid payload")
		return err
	}

	// Update the job details
	err = s.repo.UpdateWorkflow(
		ctx,
		req.GetId(),
		req.GetUserId(),
		req.GetName(),
		req.GetPayload(),
		req.GetInterval(),
		req.GetMaxConsecutiveJobFailuresAllowed(),
	)
	if err != nil {
		return err
	}

	// Invalidate the cache in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
		defer cancel()

		// Invalidate specific workflow cache
		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

		// Invalidate all list entries for the user
		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)
	}()

	return nil
}

// UpdateWorkflowBuildStatusRequest holds the request parameters for updating a job build status.
type UpdateWorkflowBuildStatusRequest struct {
	ID          string `validate:"required"`
	UserID      string `validate:"required"`
	BuildStatus string `validate:"required"`
}

// UpdateWorkflowBuildStatus updates the job build status.
func (s *Service) UpdateWorkflowBuildStatus(ctx context.Context, req *workflowspb.UpdateWorkflowBuildStatusRequest) (err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("user_id", req.GetUserId()),
		zap.String("workflow_id", req.GetId()),
		zap.String("build_status", req.GetBuildStatus()),
	)
	ctx, span := s.tp.Start(ctx, "Service.UpdateWorkflowBuildStatus")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&UpdateWorkflowBuildStatusRequest{
		ID:          req.GetId(),
		UserID:      req.GetUserId(),
		BuildStatus: req.GetBuildStatus(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Validate the job build status
	err = validateWorkflowBuildStatus(req.GetBuildStatus())
	if err != nil {
		return err
	}

	// Update the job build status
	err = s.repo.UpdateWorkflowBuildStatus(
		ctx,
		req.GetId(),
		req.GetUserId(),
		req.GetBuildStatus(),
	)
	if err != nil {
		return err
	}

	// Invalidate the cache in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
		defer cancel()

		// Invalidate specific workflow cache
		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

		// Invalidate all list entries for the user
		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)
	}()

	return err
}

// GetWorkflowRequest holds the request parameters for getting a job.
type GetWorkflowRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// GetWorkflow returns the job details by ID and user ID.
func (s *Service) GetWorkflow(ctx context.Context, req *workflowspb.GetWorkflowRequest) (res *workflowsmodel.GetWorkflowResponse, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.GetWorkflow"),
	)
	ctx, span := s.tp.Start(ctx, "Service.GetWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&GetWorkflowRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check if the workflow is cached
	cachedKey := fmt.Sprintf("workflow:%s:%s", req.GetUserId(), req.GetId())
	cacheRes, cacheErr := s.cache.Get(ctx, cachedKey, &workflowsmodel.GetWorkflowResponse{})
	if cacheErr != nil {
		if errors.Is(cacheErr, context.DeadlineExceeded) || errors.Is(cacheErr, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, cacheErr.Error())
			return nil, err
		}
	} else {
		// Cache hit, return cached response
		//nolint:errcheck,forcetypeassert // Ignore error as we are just reading from cache
		return cacheRes.(*workflowsmodel.GetWorkflowResponse), nil
	}

	// Get the job details
	res, err = s.repo.GetWorkflow(ctx, req.GetId(), req.GetUserId())
	if err != nil {
		return nil, err
	}

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
		defer cancel()

		// Invalidate all list entries for the user
		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)

		// Cache the workflow details
		if setErr := s.cache.Set(bgCtx, cachedKey, res, defaultExpirationTTL); setErr != nil {
			logger.Warn("failed to cache workflow",
				zap.String("user_id", req.GetUserId()),
				zap.String("cache_key", cachedKey),
				zap.Error(setErr),
			)
		} else if logger.Core().Enabled(zap.DebugLevel) {
			logger.Debug("cached workflow",
				zap.String("user_id", req.GetUserId()),
				zap.String("cache_key", cachedKey),
			)
		}
	}()

	return res, nil
}

// GetWorkflowByIDRequest holds the request parameters for getting a job by ID.
type GetWorkflowByIDRequest struct {
	ID string `validate:"required"`
}

// GetWorkflowByID returns the job details by ID.
// This is a internal API and we don't want the cached workflow details, so we don't use the cache here.
func (s *Service) GetWorkflowByID(ctx context.Context, req *workflowspb.GetWorkflowByIDRequest) (res *workflowsmodel.GetWorkflowByIDResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.GetWorkflowByID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&GetWorkflowByIDRequest{
		ID: req.GetId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Get the job details
	res, err = s.repo.GetWorkflowByID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}

	return res, nil
}

// IncrementWorkflowConsecutiveJobFailuresCountRequest holds the request parameters for incrementing the job consecutive failures count.
type IncrementWorkflowConsecutiveJobFailuresCountRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// IncrementWorkflowConsecutiveJobFailuresCount increments the job consecutive failures count.
func (s *Service) IncrementWorkflowConsecutiveJobFailuresCount(
	ctx context.Context,
	req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest,
) (thresholdReached bool, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.IncrementWorkflowConsecutiveJobFailuresCount"),
	)
	ctx, span := s.tp.Start(ctx, "Service.IncrementWorkflowConsecutiveJobFailuresCount")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&IncrementWorkflowConsecutiveJobFailuresCountRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return false, err
	}

	// Increment the job consecutive failures count
	thresholdReached, err = s.repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, req.GetId(), req.GetUserId())
	if err != nil {
		return false, err
	}

	// Invalidate the cache in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
		defer cancel()

		// Invalidate specific workflow cache
		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

		// Invalidate all list entries for the user
		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)
	}()

	return thresholdReached, nil
}

// ResetWorkflowConsecutiveJobFailuresCountRequest holds the request parameters for resetting the job consecutive failures count.
type ResetWorkflowConsecutiveJobFailuresCountRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// ResetWorkflowConsecutiveJobFailuresCount resets the job consecutive failures count.
//
//nolint:dupl // It's ok to have duplicate code here as the logic is similar to other methods.
func (s *Service) ResetWorkflowConsecutiveJobFailuresCount(ctx context.Context, req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) (err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.ResetWorkflowConsecutiveJobFailuresCount"),
	)
	ctx, span := s.tp.Start(ctx, "Service.ResetWorkflowConsecutiveJobFailuresCount")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&ResetWorkflowConsecutiveJobFailuresCountRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Reset the job consecutive failures count
	err = s.repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, req.GetId(), req.GetUserId())
	if err != nil {
		return err
	}

	// Invalidate the cache in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
		defer cancel()

		// Invalidate specific workflow cache
		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

		// Invalidate all list entries for the user
		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)
	}()

	return nil
}

// TerminateWorkflowRequest holds the request parameters for terminating a job.
type TerminateWorkflowRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// TerminateWorkflow terminates a job.
//
//nolint:dupl // It's ok to have duplicate code here as the logic is similar to other methods.
func (s *Service) TerminateWorkflow(ctx context.Context, req *workflowspb.TerminateWorkflowRequest) (err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.TerminateWorkflow"),
	)
	ctx, span := s.tp.Start(ctx, "Service.TerminateWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&TerminateWorkflowRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Terminate the job
	err = s.repo.TerminateWorkflow(ctx, req.GetId(), req.GetUserId())
	if err != nil {
		return err
	}

	// Invalidate the cache in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
		defer cancel()

		// Invalidate specific workflow cache
		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

		// Invalidate all list entries for the user
		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)
	}()

	return nil
}

// DeleteWorkflowRequest holds the request parameters for deleting a workflow.
type DeleteWorkflowRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// DeleteWorkflow deletes a workflow.
//
//nolint:dupl // It's ok to have duplicate code here as the logic is similar to other methods.
func (s *Service) DeleteWorkflow(ctx context.Context, req *workflowspb.DeleteWorkflowRequest) (err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.DeleteWorkflow"),
	)
	ctx, span := s.tp.Start(ctx, "Service.DeleteWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&DeleteWorkflowRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Delete the workflow
	err = s.repo.DeleteWorkflow(ctx, req.GetId(), req.GetUserId())
	if err != nil {
		return err
	}

	// Invalidate the cache in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
		defer cancel()

		// Invalidate specific workflow cache
		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

		// Invalidate all list entries for the user
		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)
	}()

	return nil
}

// ListWorkflowsRequest holds the request parameters for listing workflows by user ID.
type ListWorkflowsRequest struct {
	UserID  string                               `validate:"required"`
	Cursor  string                               `validate:"omitempty"`
	Filters *workflowsmodel.ListWorkflowsFilters `validate:"omitempty"`
}

// ListWorkflows returns workflows by user ID.
func (s *Service) ListWorkflows(ctx context.Context, req *workflowspb.ListWorkflowsRequest) (res *workflowsmodel.ListWorkflowsResponse, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("method", "Service.ListWorkflows"),
	)
	ctx, span := s.tp.Start(ctx, "Service.ListWorkflows")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	var filters *workflowsmodel.ListWorkflowsFilters
	if req.GetFilters() != nil {
		filters = &workflowsmodel.ListWorkflowsFilters{
			Query:        req.GetFilters().GetQuery(),
			Kind:         req.GetFilters().GetKind(),
			BuildStatus:  req.GetFilters().GetBuildStatus(),
			IsTerminated: req.GetFilters().GetIsTerminated(),
			IntervalMin:  req.GetFilters().GetIntervalMin(),
			IntervalMax:  req.GetFilters().GetIntervalMax(),
		}
	} else {
		filters = &workflowsmodel.ListWorkflowsFilters{}
	}

	// Validate the request
	err = s.validator.Struct(&ListWorkflowsRequest{
		UserID:  req.GetUserId(),
		Cursor:  req.GetCursor(),
		Filters: filters,
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate the cursor
	var cursor string
	if req.GetCursor() != "" {
		cursor, err = decodeCursor(req.GetCursor())
		if err != nil {
			err = status.Errorf(codes.InvalidArgument, "invalid cursor: %v", err)
			return nil, err
		}
	}

	// Validate the filters
	if err = validateFilters(filters); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid filters: %v", err)
		return nil, err
	}

	cacheKey := generateListWorkflowsCacheKey(req.GetUserId(), req.GetCursor(), filters)

	// Check for cached response
	cacheRes, cacheErr := s.cache.Get(ctx, cacheKey, &workflowsmodel.ListWorkflowsResponse{})
	if cacheErr != nil {
		if errors.Is(cacheErr, context.DeadlineExceeded) || errors.Is(cacheErr, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, cacheErr.Error())
			return nil, err
		}
	} else {
		// Cache hit, return cached response
		//nolint:errcheck,forcetypeassert // Ignore error as we are just reading from cache
		return cacheRes.(*workflowsmodel.ListWorkflowsResponse), nil
	}

	// List all workflows by user ID
	res, err = s.repo.ListWorkflows(ctx, req.GetUserId(), cursor, filters)
	if err != nil {
		return nil, err
	}

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheTimeout)
		defer cancel()

		if setErr := s.cache.Set(bgCtx, cacheKey, res, defaultExpirationTTL); setErr != nil {
			logger.Warn("failed to cache workflows list",
				zap.String("user_id", req.GetUserId()),
				zap.String("cache_key", cacheKey),
				zap.Error(setErr),
			)
		} else if logger.Core().Enabled(zap.DebugLevel) {
			logger.Debug("cached workflows list",
				zap.String("user_id", req.GetUserId()),
				zap.String("cache_key", cacheKey),
			)
		}
	}()

	return res, nil
}

func validateWorkflowBuildStatus(s string) error {
	switch s {
	case workflowsmodel.WorkflowBuildStatusQueued.ToString(),
		workflowsmodel.WorkflowBuildStatusStarted.ToString(),
		workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
		workflowsmodel.WorkflowBuildStatusFailed.ToString(),
		workflowsmodel.WorkflowBuildStatusCanceled.ToString():
		return nil
	default:
		return status.Errorf(codes.InvalidArgument, "invalid build status: %s", s)
	}
}

func validateKind(k string) error {
	switch k {
	case workflowsmodel.KindHeartbeat.ToString(),
		workflowsmodel.KindContainer.ToString():
		return nil
	default:
		return status.Errorf(codes.InvalidArgument, "invalid kind: %s", k)
	}
}

func validateFilters(filters *workflowsmodel.ListWorkflowsFilters) error {
	if filters == nil {
		return nil
	}

	if filters.Kind != "" {
		if err := validateKind(filters.Kind); err != nil {
			return err
		}
	}

	if filters.BuildStatus != "" {
		if err := validateWorkflowBuildStatus(filters.BuildStatus); err != nil {
			return err
		}
	}

	if filters.IntervalMin < 0 {
		return status.Errorf(codes.InvalidArgument, "invalid interval_min: %d", filters.IntervalMin)
	}

	if filters.IntervalMax < 0 {
		return status.Errorf(codes.InvalidArgument, "invalid interval_max: %d", filters.IntervalMax)
	}

	if filters.IntervalMin > filters.IntervalMax {
		return status.Errorf(codes.InvalidArgument, "invalid interval_min and interval_max: %d > %d", filters.IntervalMin, filters.IntervalMax)
	}

	return nil
}

func decodeCursor(token string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}

	return string(decoded), nil
}

func generateListWorkflowsCacheKey(userID, cursor string, filters *workflowsmodel.ListWorkflowsFilters) string {
	allFilters := []string{
		fmt.Sprintf("query=%s", filters.Query),
		fmt.Sprintf("kind=%s", filters.Kind),
		fmt.Sprintf("build_status=%s", filters.BuildStatus),
		fmt.Sprintf("is_terminated=%v", filters.IsTerminated),
		fmt.Sprintf("interval_min=%d", filters.IntervalMin),
		fmt.Sprintf("interval_max=%d", filters.IntervalMax),
	}

	return fmt.Sprintf("workflows:%s:cursor=%s&%s", userID, cursor, strings.Join(allFilters, "&"))
}

// invalidateWorkflowCache handles cache invalidation for a specific workflow for a user.
func (s *Service) invalidateWorkflowCache(ctx context.Context, workflowID, userID string, logger *zap.Logger) {
	// Invalidate specific workflow cache
	// The key is in the format "workflow:{user_id}:{job_id}".
	cacheKey := fmt.Sprintf("workflow:%s:%s", userID, workflowID)
	if err := s.cache.Delete(ctx, cacheKey); err != nil &&
		status.Code(err) != codes.NotFound {
		logger.Warn("failed to invalidate workflow cache",
			zap.String("user_id", userID),
			zap.String("cache_key", cacheKey),
			zap.Error(err))
	} else if logger.Core().Enabled(zap.DebugLevel) {
		logger.Debug("invalidated workflow cache",
			zap.String("user_id", userID),
			zap.String("cache_key", cacheKey))
	}
}

// invalidateWorkflowsCache handles cache invalidation for all workflows for a user.
func (s *Service) invalidateWorkflowsCache(ctx context.Context, userID string, logger *zap.Logger) {
	// Invalidate all list entries for the user
	// We use the user ID as the key, and '*' as the pattern.
	cacheKey := fmt.Sprintf("workflows:%s:*", userID)
	count, err := s.cache.DeleteByPattern(ctx, cacheKey)
	if err != nil {
		logger.Warn("failed to invalidate list caches",
			zap.String("user_id", userID),
			zap.String("cache_key", cacheKey),
			zap.Error(err))
	} else if logger.Core().Enabled(zap.DebugLevel) {
		logger.Debug("invalidated workflow list caches",
			zap.String("user_id", userID),
			zap.String("cache_key", cacheKey),
			zap.Int64("count", count))
	}
}
