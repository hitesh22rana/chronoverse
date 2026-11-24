//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package workflows

import (
	"context"
	"encoding/base64"
	"unicode/utf8"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/heartbeat"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

// Repository provides workflow related operations.
type Repository interface {
	CreateWorkflow(
		ctx context.Context,
		userID, name, payload, kind string,
		interval, maxConsecutiveJobFailuresAllowed int32,
		logRetention bool,
		idempotencyKey string,
	) (*workflowsmodel.GetWorkflowResponse, error)
	UpdateWorkflow(ctx context.Context, workflowID, userID, name, payload string, interval, maxConsecutiveJobFailuresAllowed int32, idempotencyKey string) error
	UpdateWorkflowBuildStatus(ctx context.Context, workflowID, userID, buildStatus string, generation int64, resolvedImageRef, resolvedImageDigest string) error
	GetWorkflow(ctx context.Context, workflowID, userID string) (*workflowsmodel.GetWorkflowResponse, error)
	GetWorkflowByID(ctx context.Context, workflowID string) (*workflowsmodel.GetWorkflowByIDResponse, error)
	IncrementWorkflowConsecutiveJobFailuresCount(ctx context.Context, workflowID, userID, jobID string) (bool, error)
	ResetWorkflowConsecutiveJobFailuresCount(ctx context.Context, workflowID, userID, jobID string) error
	TerminateWorkflow(ctx context.Context, workflowID, userID string) error
	DeleteWorkflow(ctx context.Context, workflowID, userID string) error
	ListWorkflows(ctx context.Context, userID, cursor string, filters *workflowsmodel.ListWorkflowsFilters) (*workflowsmodel.ListWorkflowsResponse, error)
}

// Service provides workflow related operations.
type Service struct {
	validator *validator.Validate
	tp        trace.Tracer
	repo      Repository
}

// New creates a new workflows-service.
func New(validator *validator.Validate, repo Repository) *Service {
	return &Service{
		validator: validator,
		tp:        otel.Tracer(svcpkg.Info().GetName()),
		repo:      repo,
	}
}

// CreateWorkflowRequest holds the request parameters for creating a new workflow.
type CreateWorkflowRequest struct {
	UserID                           string `validate:"required"`
	Name                             string `validate:"required,min=1,max=255"`
	Payload                          string `validate:"required"`
	Kind                             string `validate:"required"`
	Interval                         int32  `validate:"required,min=1,max=10080"` // Interval in minutes, max 1 week (10080 minutes)
	MaxConsecutiveJobFailuresAllowed int32  `validate:"required,min=3,max=100"`   // Max consecutive job failures allowed
	LogRetention                     bool   `validate:"omitempty"`                // Whether to retain logs for the workflow. Optional, defaults to false.
	IdempotencyKey                   string `validate:"required"`
}

// CreateWorkflow creates a new workflow and returns its ID.
func (s *Service) CreateWorkflow(ctx context.Context, req *workflowspb.CreateWorkflowRequest) (workflowID string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.CreateWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	logRetentionEnabled := true
	if req.LogRetention != nil {
		logRetentionEnabled = req.GetLogRetention()
	}

	err = s.validator.Struct(&CreateWorkflowRequest{
		UserID:                           req.GetUserId(),
		Name:                             req.GetName(),
		Payload:                          req.GetPayload(),
		Kind:                             req.GetKind(),
		Interval:                         req.GetInterval(),
		MaxConsecutiveJobFailuresAllowed: req.GetMaxConsecutiveJobFailuresAllowed(),
		LogRetention:                     logRetentionEnabled,
		IdempotencyKey:                   req.GetIdempotencyKey(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", status.Error(codes.InvalidArgument, err.Error())
	}

	switch req.GetKind() {
	case workflowsmodel.KindHeartbeat.ToString():
		// Heartbeat kind does not support log retention, so if log retention is enabled, return an error.
		if req.LogRetention != nil && req.GetLogRetention() {
			return "", status.Errorf(codes.InvalidArgument, "log retention is not supported for heartbeat kind")
		}
		logRetentionEnabled = false

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

	res, err := s.repo.CreateWorkflow(
		ctx,
		req.GetUserId(),
		req.GetName(),
		req.GetPayload(),
		req.GetKind(),
		req.GetInterval(),
		req.GetMaxConsecutiveJobFailuresAllowed(),
		logRetentionEnabled,
		req.GetIdempotencyKey(),
	)
	if err != nil {
		return "", err
	}

	return res.ID, nil
}

// UpdateWorkflowRequest holds the request parameters for updating a workflow.
type UpdateWorkflowRequest struct {
	ID                               string `validate:"required"`
	UserID                           string `validate:"required"`
	Name                             string `validate:"required,min=1,max=255"`
	Payload                          string `validate:"required"`
	Interval                         int32  `validate:"required,min=1,max=10080"` // Interval in minutes, max 1 week (10080 minutes)
	MaxConsecutiveJobFailuresAllowed int32  `validate:"required,min=3,max=100"`   // Max consecutive job failures allowed
	IdempotencyKey                   string `validate:"required"`
}

// UpdateWorkflow updates the workflow details.
func (s *Service) UpdateWorkflow(ctx context.Context, req *workflowspb.UpdateWorkflowRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.UpdateWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	if err = s.validator.Struct(&UpdateWorkflowRequest{
		ID:                               req.GetId(),
		UserID:                           req.GetUserId(),
		Name:                             req.GetName(),
		Payload:                          req.GetPayload(),
		Interval:                         req.GetInterval(),
		MaxConsecutiveJobFailuresAllowed: req.GetMaxConsecutiveJobFailuresAllowed(),
		IdempotencyKey:                   req.GetIdempotencyKey(),
	}); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	_, heartBeatPayloadErr := heartbeat.ExtractAndValidateHeartbeatDetails(req.GetPayload())
	_, containerPayloadErr := container.ExtractAndValidateContainerDetails(req.GetPayload())

	if heartBeatPayloadErr != nil && containerPayloadErr != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid payload")
		return err
	}

	err = s.repo.UpdateWorkflow(
		ctx,
		req.GetId(),
		req.GetUserId(),
		req.GetName(),
		req.GetPayload(),
		req.GetInterval(),
		req.GetMaxConsecutiveJobFailuresAllowed(),
		req.GetIdempotencyKey(),
	)
	if err != nil {
		return err
	}

	return nil
}

// UpdateWorkflowBuildStatusRequest holds the request parameters for updating a workflow build status.
type UpdateWorkflowBuildStatusRequest struct {
	ID          string `validate:"required"`
	UserID      string `validate:"required"`
	BuildStatus string `validate:"required"`
	Generation  int64  `validate:"required,min=1"`
}

// UpdateWorkflowBuildStatus updates the workflow build status.
func (s *Service) UpdateWorkflowBuildStatus(ctx context.Context, req *workflowspb.UpdateWorkflowBuildStatusRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.UpdateWorkflowBuildStatus")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&UpdateWorkflowBuildStatusRequest{
		ID:          req.GetId(),
		UserID:      req.GetUserId(),
		BuildStatus: req.GetBuildStatus(),
		Generation:  req.GetGeneration(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	err = validateWorkflowBuildStatus(req.GetBuildStatus())
	if err != nil {
		return err
	}

	err = s.repo.UpdateWorkflowBuildStatus(
		ctx,
		req.GetId(),
		req.GetUserId(),
		req.GetBuildStatus(),
		req.GetGeneration(),
		req.GetResolvedImageRef(),
		req.GetResolvedImageDigest(),
	)
	if err != nil {
		return err
	}

	return err
}

// GetWorkflowRequest holds the request parameters for getting a workflow.
type GetWorkflowRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// GetWorkflow returns the workflow details by ID and user ID.
func (s *Service) GetWorkflow(
	ctx context.Context,
	req *workflowspb.GetWorkflowRequest,
) (
	res *workflowsmodel.GetWorkflowResponse,
	err error,
) {
	ctx, span := s.tp.Start(ctx, "Service.GetWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&GetWorkflowRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return s.repo.GetWorkflow(ctx, req.GetId(), req.GetUserId())
}

// GetWorkflowByIDRequest holds the request parameters for getting a workflow by ID.
type GetWorkflowByIDRequest struct {
	ID string `validate:"required"`
}

// GetWorkflowByID returns the workflow details by ID.
// Internal callers use this method when they do not have an owner ID.
func (s *Service) GetWorkflowByID(
	ctx context.Context,
	req *workflowspb.GetWorkflowByIDRequest,
) (
	res *workflowsmodel.GetWorkflowByIDResponse,
	err error,
) {
	ctx, span := s.tp.Start(ctx, "Service.GetWorkflowByID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&GetWorkflowByIDRequest{
		ID: req.GetId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return s.repo.GetWorkflowByID(ctx, req.GetId())
}

// IncrementWorkflowConsecutiveJobFailuresCountRequest holds the request parameters for incrementing the job consecutive failures count.
type IncrementWorkflowConsecutiveJobFailuresCountRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
	JobID  string `validate:"required"`
}

// IncrementWorkflowConsecutiveJobFailuresCount increments the job consecutive failures count.
func (s *Service) IncrementWorkflowConsecutiveJobFailuresCount(
	ctx context.Context,
	req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest,
) (thresholdReached bool, err error) {
	ctx, span := s.tp.Start(ctx, "Service.IncrementWorkflowConsecutiveJobFailuresCount")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&IncrementWorkflowConsecutiveJobFailuresCountRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
		JobID:  req.GetJobId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return false, err
	}

	thresholdReached, err = s.repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, req.GetId(), req.GetUserId(), req.GetJobId())
	if err != nil {
		return false, err
	}

	return thresholdReached, nil
}

// ResetWorkflowConsecutiveJobFailuresCountRequest holds the request parameters for resetting the job consecutive failures count.
type ResetWorkflowConsecutiveJobFailuresCountRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
	JobID  string `validate:"required"`
}

// ResetWorkflowConsecutiveJobFailuresCount resets the job consecutive failures count.
func (s *Service) ResetWorkflowConsecutiveJobFailuresCount(
	ctx context.Context,
	req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest,
) (
	err error,
) {
	ctx, span := s.tp.Start(ctx, "Service.ResetWorkflowConsecutiveJobFailuresCount")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&ResetWorkflowConsecutiveJobFailuresCountRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
		JobID:  req.GetJobId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	err = s.repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, req.GetId(), req.GetUserId(), req.GetJobId())
	if err != nil {
		return err
	}

	return nil
}

// TerminateWorkflowRequest holds the request parameters for terminating a workflow.
type TerminateWorkflowRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// TerminateWorkflow terminates a workflow.
func (s *Service) TerminateWorkflow(ctx context.Context, req *workflowspb.TerminateWorkflowRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.TerminateWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&TerminateWorkflowRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	err = s.repo.TerminateWorkflow(ctx, req.GetId(), req.GetUserId())
	if err != nil {
		return err
	}

	return nil
}

// DeleteWorkflowRequest holds the request parameters for deleting a workflow.
type DeleteWorkflowRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// DeleteWorkflow deletes a workflow.
func (s *Service) DeleteWorkflow(ctx context.Context, req *workflowspb.DeleteWorkflowRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.DeleteWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&DeleteWorkflowRequest{
		ID:     req.GetId(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	err = s.repo.DeleteWorkflow(ctx, req.GetId(), req.GetUserId())
	if err != nil {
		return err
	}

	return nil
}

// ListWorkflowsRequest holds the request parameters for listing workflows by user ID.
type ListWorkflowsRequest struct {
	UserID  string                               `validate:"required"`
	Cursor  string                               `validate:"omitempty"`
	Filters *workflowsmodel.ListWorkflowsFilters `validate:"omitempty"`
}

// ListWorkflows returns workflows by user ID.
func (s *Service) ListWorkflows(
	ctx context.Context,
	req *workflowspb.ListWorkflowsRequest,
) (
	res *workflowsmodel.ListWorkflowsResponse,
	err error,
) {
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

	err = s.validator.Struct(&ListWorkflowsRequest{
		UserID:  req.GetUserId(),
		Cursor:  req.GetCursor(),
		Filters: filters,
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	var cursor string
	if req.GetCursor() != "" {
		cursor, err = decodeCursor(req.GetCursor())
		if err != nil {
			err = status.Errorf(codes.InvalidArgument, "invalid cursor: %v", err)
			return nil, err
		}
	}

	if err = validateFilters(filters); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid filters: %v", err)
		return nil, err
	}

	return s.repo.ListWorkflows(ctx, req.GetUserId(), cursor, filters)
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

	if utf8.RuneCountInString(filters.Query) > 100 {
		return status.Errorf(codes.InvalidArgument, "invalid query: exceeds 100 characters")
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

	if filters.IntervalMax > 0 && filters.IntervalMin > filters.IntervalMax {
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
