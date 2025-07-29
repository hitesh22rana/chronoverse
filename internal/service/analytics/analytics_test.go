package analytics_test

import (
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	"github.com/hitesh22rana/chronoverse/internal/service/analytics"
	analyticsmock "github.com/hitesh22rana/chronoverse/internal/service/analytics/mock"
)

func TestGetUserAnalytics(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := analyticsmock.NewMockRepository(ctrl)

	// Create a new service
	s := analytics.New(validator.New(), repo)

	// Test cases
	tests := []struct {
		name  string
		req   *analyticspb.GetUserAnalyticsRequest
		mock  func(req *analyticspb.GetUserAnalyticsRequest)
		want  *analyticsmodel.GetUserAnalyticsResponse
		isErr bool
	}{
		{
			name: "success",
			req: &analyticspb.GetUserAnalyticsRequest{
				UserId: "user1",
			},
			mock: func(req *analyticspb.GetUserAnalyticsRequest) {
				repo.EXPECT().GetUserAnalytics(
					gomock.Any(),
					req.GetUserId(),
				).Return(&analyticsmodel.GetUserAnalyticsResponse{
					TotalWorkflows: 10,
					TotalJobs:      200,
					TotalJoblogs:   5000,
				}, nil)
			},
			want: &analyticsmodel.GetUserAnalyticsResponse{
				TotalWorkflows: 10,
				TotalJobs:      200,
				TotalJoblogs:   5000,
			},
			isErr: false,
		},
		{
			name: "error: not found",
			req: &analyticspb.GetUserAnalyticsRequest{
				UserId: "user1",
			},
			mock: func(req *analyticspb.GetUserAnalyticsRequest) {
				repo.EXPECT().GetUserAnalytics(
					gomock.Any(),
					req.GetUserId(),
				).Return(nil, status.Errorf(codes.NotFound, "user not found"))
			},
			want:  nil,
			isErr: true,
		},
		{
			name: "error: missing user ID",
			req: &analyticspb.GetUserAnalyticsRequest{
				UserId: "",
			},
			mock:  func(_ *analyticspb.GetUserAnalyticsRequest) {},
			want:  nil,
			isErr: true,
		},
		{
			name: "error: internal error",
			req: &analyticspb.GetUserAnalyticsRequest{
				UserId: "user1",
			},
			mock: func(req *analyticspb.GetUserAnalyticsRequest) {
				repo.EXPECT().GetUserAnalytics(
					gomock.Any(),
					req.GetUserId(),
				).Return(nil, status.Errorf(codes.Internal, "internal error"))
			},
			want:  nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			analytics, err := s.GetUserAnalytics(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			assert.Equal(t, tt.want, analytics)
		})
	}
}

func TestGetWorkflowAnalytics(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := analyticsmock.NewMockRepository(ctrl)

	// Create a new service
	s := analytics.New(validator.New(), repo)

	// Test cases
	tests := []struct {
		name  string
		req   *analyticspb.GetWorkflowAnalyticsRequest
		mock  func(req *analyticspb.GetWorkflowAnalyticsRequest)
		want  *analyticsmodel.GetWorkflowAnalyticsResponse
		isErr bool
	}{
		{
			name: "success",
			req: &analyticspb.GetWorkflowAnalyticsRequest{
				UserId:     "user1",
				WorkflowId: "workflow1",
			},
			mock: func(req *analyticspb.GetWorkflowAnalyticsRequest) {
				repo.EXPECT().GetWorkflowAnalytics(
					gomock.Any(),
					req.GetUserId(),
					req.GetWorkflowId(),
				).Return(&analyticsmodel.GetWorkflowAnalyticsResponse{
					WorkflowID:                "workflow1",
					AvgJobExecutionDurationMs: 20000,
					TotalJobs:                 50,
					TotalJoblogs:              1000,
				}, nil)
			},
			want: &analyticsmodel.GetWorkflowAnalyticsResponse{
				WorkflowID:                "workflow1",
				AvgJobExecutionDurationMs: 20000,
				TotalJobs:                 50,
				TotalJoblogs:              1000,
			},
			isErr: false,
		},
		{
			name: "error: not found",
			req: &analyticspb.GetWorkflowAnalyticsRequest{
				UserId:     "user1",
				WorkflowId: "workflow1",
			},
			mock: func(req *analyticspb.GetWorkflowAnalyticsRequest) {
				repo.EXPECT().GetWorkflowAnalytics(
					gomock.Any(),
					req.GetUserId(),
					req.GetWorkflowId(),
				).Return(nil, status.Errorf(codes.NotFound, "workflow not found"))
			},
			want:  nil,
			isErr: true,
		},
		{
			name: "error: missing user ID",
			req: &analyticspb.GetWorkflowAnalyticsRequest{
				UserId:     "",
				WorkflowId: "workflow1",
			},
			mock:  func(_ *analyticspb.GetWorkflowAnalyticsRequest) {},
			want:  nil,
			isErr: true,
		},
		{
			name: "error: missing workflow ID",
			req: &analyticspb.GetWorkflowAnalyticsRequest{
				UserId:     "user1",
				WorkflowId: "",
			},
			mock:  func(_ *analyticspb.GetWorkflowAnalyticsRequest) {},
			want:  nil,
			isErr: true,
		},
		{
			name: "error: internal error",
			req: &analyticspb.GetWorkflowAnalyticsRequest{
				UserId:     "user1",
				WorkflowId: "workflow1",
			},
			mock: func(req *analyticspb.GetWorkflowAnalyticsRequest) {
				repo.EXPECT().GetWorkflowAnalytics(
					gomock.Any(),
					req.GetUserId(),
					req.GetWorkflowId(),
				).Return(nil, status.Errorf(codes.Internal, "internal error"))
			},
			want:  nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			analytics, err := s.GetWorkflowAnalytics(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			assert.Equal(t, tt.want, analytics)
		})
	}
}
