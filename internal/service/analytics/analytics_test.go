package analytics_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	repo := analyticsmock.NewMockRepository(ctrl)

	s := analytics.New(validator.New(), repo)

	tests := []struct {
		name     string
		req      *analyticspb.GetUserAnalyticsRequest
		mock     func(req *analyticspb.GetUserAnalyticsRequest)
		want     *analyticsmodel.GetUserAnalyticsResponse
		wantCode codes.Code
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
					TotalWorkflows:            10,
					TotalJobs:                 200,
					TotalJoblogs:              5000,
					TotalJobExecutionDuration: 1000000,
				}, nil)
			},
			want: &analyticsmodel.GetUserAnalyticsResponse{
				TotalWorkflows:            10,
				TotalJobs:                 200,
				TotalJoblogs:              5000,
				TotalJobExecutionDuration: 1000000,
			},
			wantCode: codes.OK,
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
			want:     nil,
			wantCode: codes.NotFound,
		},
		{
			name: "error: missing user ID",
			req: &analyticspb.GetUserAnalyticsRequest{
				UserId: "",
			},
			mock:     func(_ *analyticspb.GetUserAnalyticsRequest) {},
			want:     nil,
			wantCode: codes.InvalidArgument,
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
			want:     nil,
			wantCode: codes.Internal,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock(tt.req)
			analytics, err := s.GetUserAnalytics(t.Context(), tt.req)
			require.Equal(t, tt.wantCode, status.Code(err), "%v", err)
			if tt.wantCode != codes.OK {
				require.Nil(t, analytics)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.want, analytics)
		})
	}
}

func TestGetUserAnalyticsSingleflight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := analyticsmock.NewMockRepository(gomock.NewController(t))
		s := analytics.New(validator.New(), repo)
		req := &analyticspb.GetUserAnalyticsRequest{UserId: "singleflight_user"}
		release := make(chan struct{})
		var calls atomic.Int32
		// Count instead of Times(1): gomock.Fatal in singleflight's goroutine would prevent result delivery.
		repo.EXPECT().GetUserAnalytics(gomock.Any(), req.GetUserId()).DoAndReturn(
			func(_ context.Context, _ string) (*analyticsmodel.GetUserAnalyticsResponse, error) {
				calls.Add(1)
				<-release
				return &analyticsmodel.GetUserAnalyticsResponse{
					TotalWorkflows: 10, TotalJobs: 200, TotalJoblogs: 5000, TotalJobExecutionDuration: 1000000,
				}, nil
			},
		).AnyTimes()

		var (
			wg      sync.WaitGroup
			results [2]*analyticsmodel.GetUserAnalyticsResponse
			errs    [2]error
		)
		for i := range results {
			wg.Go(func() { results[i], errs[i] = s.GetUserAnalytics(t.Context(), req) })
		}
		// Both callers must be blocked on the shared result before the repository can return.
		synctest.Wait()
		close(release)
		wg.Wait()

		require.Equal(t, int32(1), calls.Load())
		want := &analyticsmodel.GetUserAnalyticsResponse{
			TotalWorkflows: 10, TotalJobs: 200, TotalJoblogs: 5000, TotalJobExecutionDuration: 1000000,
		}
		for i := range results {
			require.NoError(t, errs[i])
			require.Equal(t, want, results[i])
		}
	})
}

func TestGetWorkflowAnalytics(t *testing.T) {
	ctrl := gomock.NewController(t)

	repo := analyticsmock.NewMockRepository(ctrl)

	s := analytics.New(validator.New(), repo)

	tests := []struct {
		name     string
		req      *analyticspb.GetWorkflowAnalyticsRequest
		mock     func(req *analyticspb.GetWorkflowAnalyticsRequest)
		want     *analyticsmodel.GetWorkflowAnalyticsResponse
		wantCode codes.Code
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
					TotalJobExecutionDuration: 20000,
					TotalJobs:                 50,
					TotalJoblogs:              1000,
				}, nil)
			},
			want: &analyticsmodel.GetWorkflowAnalyticsResponse{
				WorkflowID:                "workflow1",
				TotalJobExecutionDuration: 20000,
				TotalJobs:                 50,
				TotalJoblogs:              1000,
			},
			wantCode: codes.OK,
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
			want:     nil,
			wantCode: codes.NotFound,
		},
		{
			name: "error: missing user ID",
			req: &analyticspb.GetWorkflowAnalyticsRequest{
				UserId:     "",
				WorkflowId: "workflow1",
			},
			mock:     func(_ *analyticspb.GetWorkflowAnalyticsRequest) {},
			want:     nil,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "error: missing workflow ID",
			req: &analyticspb.GetWorkflowAnalyticsRequest{
				UserId:     "user1",
				WorkflowId: "",
			},
			mock:     func(_ *analyticspb.GetWorkflowAnalyticsRequest) {},
			want:     nil,
			wantCode: codes.InvalidArgument,
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
			want:     nil,
			wantCode: codes.Internal,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock(tt.req)
			analytics, err := s.GetWorkflowAnalytics(t.Context(), tt.req)
			require.Equal(t, tt.wantCode, status.Code(err), "%v", err)
			if tt.wantCode != codes.OK {
				require.Nil(t, analytics)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.want, analytics)
		})
	}
}
