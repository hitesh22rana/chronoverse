package analytics_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	"github.com/hitesh22rana/chronoverse/internal/service/analytics"
	analyticsmock "github.com/hitesh22rana/chronoverse/internal/service/analytics/mock"
	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
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

func TestAnalyticsReadsKeepEachCallersDeadline(t *testing.T) {
	for _, method := range []string{"user", "workflow"} {
		t.Run(method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				repo := analyticsmock.NewMockRepository(gomock.NewController(t))
				service := analytics.New(validator.New(), repo)
				ready := make(chan struct{})
				waitForData := func(ctx context.Context) error {
					select {
					case <-ctx.Done():
						return status.FromContextError(ctx.Err()).Err()
					case <-ready:
						return nil
					}
				}

				var read func(context.Context) error
				if method == "user" {
					request := &analyticspb.GetUserAnalyticsRequest{UserId: "user"}
					repo.EXPECT().GetUserAnalytics(gomock.Any(), "user").DoAndReturn(
						func(ctx context.Context, _ string) (*analyticsmodel.GetUserAnalyticsResponse, error) {
							if err := waitForData(ctx); err != nil {
								return nil, err
							}
							return &analyticsmodel.GetUserAnalyticsResponse{}, nil
						},
					).AnyTimes()
					read = func(ctx context.Context) error {
						_, err := service.GetUserAnalytics(ctx, request)
						return err
					}
				} else {
					request := &analyticspb.GetWorkflowAnalyticsRequest{UserId: "user", WorkflowId: "workflow"}
					repo.EXPECT().GetWorkflowAnalytics(gomock.Any(), "user", "workflow").DoAndReturn(
						func(ctx context.Context, _, _ string) (*analyticsmodel.GetWorkflowAnalyticsResponse, error) {
							if err := waitForData(ctx); err != nil {
								return nil, err
							}
							return &analyticsmodel.GetWorkflowAnalyticsResponse{}, nil
						},
					).AnyTimes()
					read = func(ctx context.Context) error {
						_, err := service.GetWorkflowAnalytics(ctx, request)
						return err
					}
				}

				const budget = 500 * time.Millisecond
				firstCtx, cancelFirst := context.WithTimeout(t.Context(), budget)
				defer cancelFirst()
				first := make(chan error, 1)
				go func() { first <- read(firstCtx) }()
				synctest.Wait()

				time.Sleep(250 * time.Millisecond)
				secondCtx, cancelSecond := context.WithTimeout(t.Context(), budget)
				defer cancelSecond()
				second := make(chan error, 1)
				go func() { second <- read(secondCtx) }()
				synctest.Wait()

				// Data arrives after the first deadline and before the second.
				time.Sleep(350 * time.Millisecond)
				close(ready)
				require.Equal(t, codes.DeadlineExceeded, status.Code(<-first))
				require.NoError(t, <-second)
			})
		})
	}
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
