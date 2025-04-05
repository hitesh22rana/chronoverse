//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package workflows

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	defaultExpiration        = time.Hour
	cacheInvalidationTimeout = time.Second * 5

	noCursor = "no_cursor"
)

// Repository provides job related operations.
type Repository interface {
	CreateWorkflow(ctx context.Context, userID, name, payload, kind string, interval, maxConsecutiveJobFailuresAllowed int32) (string, error)
	UpdateWorkflow(ctx context.Context, workflowID, userID, name, payload string, interval, maxConsecutiveJobFailuresAllowed int32) error
	UpdateWorkflowBuildStatus(ctx context.Context, workflowID, userID, buildStatus string) error
	GetWorkflow(ctx context.Context, workflowID, userID string) (*workflowsmodel.GetWorkflowResponse, error)
	GetWorkflowByID(ctx context.Context, workflowID string) (*workflowsmodel.GetWorkflowByIDResponse, error)
	IncrementWorkflowConsecutiveJobFailuresCount(ctx context.Context, workflowID, userID string) (bool, error)
	ResetWorkflowConsecutiveJobFailuresCount(ctx context.Context, workflowID, userID string) error
	TerminateWorkflow(ctx context.Context, workflowID, userID string) error
	ListWorkflows(ctx context.Context, userID, cursor string) (*workflowsmodel.ListWorkflowsResponse, error)
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
	Interval                         int32  `validate:"required"`
	MaxConsecutiveJobFailuresAllowed int32  `validate:"omitempty"`
}

// CreateWorkflow a new job.
func (s *Service) CreateWorkflow(ctx context.Context, req *workflowspb.CreateWorkflowRequest) (jobID string, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("user_id", req.GetUserId()),
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

	// CreateWorkflow the job
	jobID, err = s.repo.CreateWorkflow(
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
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheInvalidationTimeout)
		defer cancel()

		// Cache the workflow details
		// We use the combination of user ID and job ID as the key to uniquely identify the workflow.
		// The key is in the format "workflow:{user_id}:{job_id}".
		cacheKey := fmt.Sprintf("workflow:%s:%s", req.GetUserId(), jobID)
		if setErr := s.cache.Set(bgCtx, cacheKey, jobID, defaultExpiration); setErr != nil {
			logger.Warn("failed to cache workflow",
				zap.String("cache_key", cacheKey),
				zap.Error(setErr),
			)
		} else if logger.Core().Enabled(zap.DebugLevel) {
			logger.Debug("cached workflow",
				zap.String("cache_key", cacheKey),
			)
		}

		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)
	}()

	return jobID, nil
}

// UpdateWorkflowRequest holds the request parameters for updating a job.
type UpdateWorkflowRequest struct {
	ID                               string `validate:"required"`
	UserID                           string `validate:"required"`
	Name                             string `validate:"required"`
	Payload                          string `validate:"required"`
	Interval                         int32  `validate:"required"`
	MaxConsecutiveJobFailuresAllowed int32  `validate:"omitempty"`
}

// UpdateWorkflow updates the job details.
func (s *Service) UpdateWorkflow(ctx context.Context, req *workflowspb.UpdateWorkflowRequest) (err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("user_id", req.GetUserId()),
		zap.String("workflow_id", req.GetId()),
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

	// Validate the JSON payload
	var _payload map[string]any
	if err = json.Unmarshal([]byte(req.GetPayload()), &_payload); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid payload: %v", err)
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

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheInvalidationTimeout)
		defer cancel()

		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

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

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheInvalidationTimeout)
		defer cancel()

		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

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
		zap.String("user_id", req.GetUserId()),
		zap.String("workflow_id", req.GetId()),
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
	if cacheRes, cacheErr := s.cache.Get(ctx, cachedKey, &workflowsmodel.GetWorkflowResponse{}); cacheErr == nil {
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
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheInvalidationTimeout)
		defer cancel()

		if setErr := s.cache.Set(bgCtx, cachedKey, res, defaultExpiration); setErr != nil {
			logger.Warn("failed to cache workflow",
				zap.String("cache_key", cachedKey),
				zap.Error(setErr),
			)
		} else if logger.Core().Enabled(zap.DebugLevel) {
			logger.Debug("cached workflow",
				zap.String("cache_key", cachedKey),
			)
		}

		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)
	}()

	return res, nil
}

// GetWorkflowByIDRequest holds the request parameters for getting a job by ID.
type GetWorkflowByIDRequest struct {
	ID string `validate:"required"`
}

// GetWorkflowByID returns the job details by ID.
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
		zap.String("user_id", req.GetUserId()),
		zap.String("workflow_id", req.GetId()),
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

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheInvalidationTimeout)
		defer cancel()

		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

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
		zap.String("user_id", req.GetUserId()),
		zap.String("workflow_id", req.GetId()),
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

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheInvalidationTimeout)
		defer cancel()

		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

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
		zap.String("user_id", req.GetUserId()),
		zap.String("workflow_id", req.GetId()),
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

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		// Cache invalidation for the following:
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheInvalidationTimeout)
		defer cancel()

		s.invalidateWorkflowCache(bgCtx, req.GetId(), req.GetUserId(), logger)

		s.invalidateWorkflowsCache(bgCtx, req.GetUserId(), logger)
	}()

	return nil
}

// ListWorkflowsRequest holds the request parameters for listing workflows by user ID.
type ListWorkflowsRequest struct {
	UserID string `validate:"required"`
	Cursor string `validate:"omitempty"`
}

// ListWorkflows returns workflows by user ID.
func (s *Service) ListWorkflows(ctx context.Context, req *workflowspb.ListWorkflowsRequest) (res *workflowsmodel.ListWorkflowsResponse, err error) {
	logger := loggerpkg.FromContext(ctx).With(
		zap.String("user_id", req.GetUserId()),
		zap.String("cursor", req.GetCursor()),
	)
	ctx, span := s.tp.Start(ctx, "Service.ListWorkflows")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&ListWorkflowsRequest{
		UserID: req.GetUserId(),
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

	cachedCursorKey := req.GetCursor()
	if cachedCursorKey == "" {
		cachedCursorKey = noCursor
	}

	cacheKey := fmt.Sprintf("workflows:%s:%s", req.GetUserId(), cachedCursorKey)

	// Check for cached response
	if cacheRes, cacheErr := s.cache.Get(ctx, cacheKey, &workflowsmodel.ListWorkflowsResponse{}); cacheErr == nil {
		// Cache hit, return cached response
		//nolint:errcheck,forcetypeassert // Ignore error as we are just reading from cache
		return cacheRes.(*workflowsmodel.ListWorkflowsResponse), nil
	}

	// List all workflows by user ID
	res, err = s.repo.ListWorkflows(ctx, req.GetUserId(), cursor)
	if err != nil {
		return nil, err
	}

	// Cache the response in the background
	// This is a fire-and-forget operation, so we don't wait for it to complete.
	//nolint:contextcheck // Ignore context check as we are using a new context
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), cacheInvalidationTimeout)
		defer cancel()

		if setErr := s.cache.Set(bgCtx, cacheKey, res, defaultExpiration); setErr != nil {
			logger.Warn("failed to cache workflows list",
				zap.Error(setErr),
			)
		} else if logger.Core().Enabled(zap.DebugLevel) {
			logger.Debug("cached workflows list",
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

func decodeCursor(token string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}

	return string(decoded), nil
}

// invalidateWorkflowCache handles cache invalidation for a specific workflow for a user.
func (s *Service) invalidateWorkflowCache(ctx context.Context, workflowID, userID string, logger *zap.Logger) {
	// Invalidate specific workflow cache
	// The key is in the format "workflow:{user_id}:{job_id}".
	cacheKey := fmt.Sprintf("workflow:%s:%s", userID, workflowID)
	if err := s.cache.Delete(ctx, cacheKey); err != nil &&
		status.Code(err) != codes.NotFound {
		logger.Warn("failed to invalidate workflow cache",
			zap.String("key", cacheKey),
			zap.Error(err))
	} else if logger.Core().Enabled(zap.DebugLevel) {
		logger.Debug("invalidated workflow cache",
			zap.String("key", cacheKey))
	}
}

// invalidateWorkflowsCache handles cache invalidation for all workflows for a user.
func (s *Service) invalidateWorkflowsCache(ctx context.Context, userID string, logger *zap.Logger) {
	// Invalidate all list entries for the user
	// We use the user ID as the key, and '*' as the pattern.
	patternKey := fmt.Sprintf("workflows:%s:*", userID)
	count, err := s.cache.DeleteByPattern(ctx, patternKey)
	if err != nil {
		logger.Warn("failed to invalidate list caches",
			zap.String("pattern", patternKey),
			zap.Error(err))
	} else if logger.Core().Enabled(zap.DebugLevel) {
		logger.Debug("invalidated workflow list caches",
			zap.Int64("count", count))
	}
}
