//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package analytics

import (
	"context"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
)

// Repository provides analytics related operations.
type Repository interface {
	GetUserAnalytics(ctx context.Context, userID string) (*analyticsmodel.GetUserAnalyticsResponse, error)
	GetWorkflowAnalytics(ctx context.Context, userID, workflowID string) (*analyticsmodel.GetWorkflowAnalyticsResponse, error)
}

// Service provides analytics service operations.
type Service struct {
	tp        trace.Tracer
	validator *validator.Validate
	repo      Repository
}

// New creates a new analytics service.
func New(validator *validator.Validate, repo Repository) *Service {
	return &Service{
		tp:        otel.Tracer(svcpkg.Info().GetName()),
		validator: validator,
		repo:      repo,
	}
}

// GetUserAnalyticsRequest retrieves analytics data for a specific user.
type GetUserAnalyticsRequest struct {
	UserID string `json:"user_id" validate:"required"`
}

// GetUserAnalytics retrieves analytics data for a specific user.
func (s *Service) GetUserAnalytics(ctx context.Context, req *analyticspb.GetUserAnalyticsRequest) (res *analyticsmodel.GetUserAnalyticsResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.GetUserAnalytics")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&GetUserAnalyticsRequest{
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Error(codes.InvalidArgument, "invalid request parameters")
		return nil, err
	}

	res, err = s.repo.GetUserAnalytics(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}

	return res, nil
}

// GetWorkflowAnalyticsRequest retrieves analytics data for a specific workflow.
type GetWorkflowAnalyticsRequest struct {
	UserID     string `json:"user_id" validate:"required"`
	WorkflowID string `json:"workflow_id" validate:"required"`
}

// GetWorkflowAnalytics retrieves analytics data for a specific workflow.
func (s *Service) GetWorkflowAnalytics(ctx context.Context, req *analyticspb.GetWorkflowAnalyticsRequest) (res *analyticsmodel.GetWorkflowAnalyticsResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.GetWorkflowAnalytics")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	err = s.validator.Struct(&GetWorkflowAnalyticsRequest{
		UserID:     req.GetUserId(),
		WorkflowID: req.GetWorkflowId(),
	})
	if err != nil {
		err = status.Error(codes.InvalidArgument, "invalid request parameters")
		return nil, err
	}

	res, err = s.repo.GetWorkflowAnalytics(ctx, req.GetUserId(), req.GetWorkflowId())
	if err != nil {
		return nil, err
	}

	return res, nil
}
