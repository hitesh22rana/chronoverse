//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package workflows

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides job related operations.
type Repository interface {
	CreateWorkflow(ctx context.Context, userID, name, payload, kind string, interval int32) (string, error)
	UpdateWorkflow(ctx context.Context, jobID, userID, name, payload string, interval int32) error
	UpdateWorkflowBuildStatus(ctx context.Context, jobID, buildStatus string) error
	GetWorkflow(ctx context.Context, jobID, userID string) (*workflowsmodel.GetWorkflowResponse, error)
	GetWorkflowByID(ctx context.Context, jobID string) (*workflowsmodel.GetWorkflowByIDResponse, error)
	TerminateWorkflow(ctx context.Context, jobID, userID string) error
	ListWorkflows(ctx context.Context, userID, cursor string) (*workflowsmodel.ListWorkflowsResponse, error)
}

// Service provides job related operations.
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

// CreateWorkflowRequest holds the request parameters for creating a new job.
type CreateWorkflowRequest struct {
	UserID   string `validate:"required"`
	Name     string `validate:"required"`
	Payload  string `validate:"required"`
	Kind     string `validate:"required"`
	Interval int32  `validate:"required"`
}

// CreateWorkflow a new job.
func (s *Service) CreateWorkflow(ctx context.Context, req *workflowspb.CreateWorkflowRequest) (jobID string, err error) {
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

	// CreateWorkflow the job
	jobID, err = s.repo.CreateWorkflow(
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

// UpdateWorkflowRequest holds the request parameters for updating a job.
type UpdateWorkflowRequest struct {
	ID       string `validate:"required"`
	UserID   string `validate:"required"`
	Name     string `validate:"required"`
	Payload  string `validate:"required"`
	Interval int32  `validate:"required"`
}

// UpdateWorkflow updates the job details.
func (s *Service) UpdateWorkflow(ctx context.Context, req *workflowspb.UpdateWorkflowRequest) (err error) {
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
	err = s.repo.UpdateWorkflow(
		ctx,
		req.GetId(),
		req.GetUserId(),
		req.GetName(),
		req.GetPayload(),
		req.GetInterval(),
	)

	return err
}

// UpdateWorkflowBuildStatusRequest holds the request parameters for updating a job build status.
type UpdateWorkflowBuildStatusRequest struct {
	ID          string `validate:"required"`
	BuildStatus string `validate:"required"`
}

// UpdateWorkflowBuildStatus updates the job build status.
func (s *Service) UpdateWorkflowBuildStatus(ctx context.Context, req *workflowspb.UpdateWorkflowBuildStatusRequest) (err error) {
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
		req.GetBuildStatus(),
	)

	return err
}

// GetWorkflowRequest holds the request parameters for getting a job.
type GetWorkflowRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// GetWorkflow returns the job details by ID and user ID.
func (s *Service) GetWorkflow(ctx context.Context, req *workflowspb.GetWorkflowRequest) (res *workflowsmodel.GetWorkflowResponse, err error) {
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

	// Get the job details
	res, err = s.repo.GetWorkflow(ctx, req.GetId(), req.GetUserId())
	if err != nil {
		return nil, err
	}

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

// ScheduleWorkflowRequest holds the request parameters for scheduling a job.
type ScheduleWorkflowRequest struct {
	WorkflowID  string `validate:"required"`
	UserID      string `validate:"required"`
	ScheduledAt string `validate:"required"`
}

// TerminateWorkflowRequest holds the request parameters for terminating a job.
type TerminateWorkflowRequest struct {
	ID     string `validate:"required"`
	UserID string `validate:"required"`
}

// TerminateWorkflow terminates a job.
func (s *Service) TerminateWorkflow(ctx context.Context, req *workflowspb.TerminateWorkflowRequest) (err error) {
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

	return err
}

// ListWorkflowsRequest holds the request parameters for listing workflows by user ID.
type ListWorkflowsRequest struct {
	UserID string `validate:"required"`
	Cursor string `validate:"omitempty"`
}

// ListWorkflows returns workflows by user ID.
func (s *Service) ListWorkflows(ctx context.Context, req *workflowspb.ListWorkflowsRequest) (res *workflowsmodel.ListWorkflowsResponse, err error) {
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

	// Validate the next page token
	var cursor string
	if req.GetCursor() != "" {
		cursor, err = decodeCursor(req.GetCursor())
		if err != nil {
			err = status.Errorf(codes.InvalidArgument, "invalid next page token: %v", err)
			return nil, err
		}
	}

	// List all workflows by user ID
	res, err = s.repo.ListWorkflows(ctx, req.GetUserId(), cursor)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// ListScheduledWorkflowsRequest holds the request parameters for listing scheduled workflows.
type ListScheduledWorkflowsRequest struct {
	WorkflowID string `validate:"required"`
	UserID     string `validate:"required"`
	Cursor     string `validate:"omitempty"`
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
