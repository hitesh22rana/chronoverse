package analytics_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

	var (
		singleflightRepoCalls   int32
		singleflightReleaseChan chan struct{}
		singleflightLeaderRan   chan struct{}
	)

	tests := []struct {
		name     string
		req      *analyticspb.GetUserAnalyticsRequest
		mock     func(req *analyticspb.GetUserAnalyticsRequest)
		execute  func(t *testing.T, req *analyticspb.GetUserAnalyticsRequest, want *analyticsmodel.GetUserAnalyticsResponse)
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
			name: "success: singleflight deduplicates concurrent request",
			req: &analyticspb.GetUserAnalyticsRequest{
				// Own user ID, so this case's expectation cannot answer another's.
				UserId: "singleflight_user",
			},
			mock: func(req *analyticspb.GetUserAnalyticsRequest) {
				singleflightRepoCalls = 0
				singleflightReleaseChan = make(chan struct{})
				// Closed by the leader as it parks, so the wait below cannot be
				// satisfied by a call that already returned.
				singleflightLeaderRan = make(chan struct{})
				// AnyTimes, not Times(1): a second call under Times(1) makes gomock
				// t.Fatalf on the leader goroutine, which Goexits it before
				// singleflight publishes its result and hangs the package. The dedupe
				// is asserted on singleflightRepoCalls.
				repo.EXPECT().GetUserAnalytics(
					gomock.Any(),
					req.GetUserId(),
				).DoAndReturn(func(_ any, _ string) (*analyticsmodel.GetUserAnalyticsResponse, error) {
					if atomic.AddInt32(&singleflightRepoCalls, 1) == 1 {
						close(singleflightLeaderRan)
						<-singleflightReleaseChan
					}
					return &analyticsmodel.GetUserAnalyticsResponse{
						TotalWorkflows:            10,
						TotalJobs:                 200,
						TotalJoblogs:              5000,
						TotalJobExecutionDuration: 1000000,
					}, nil
				}).AnyTimes()
			},
			execute: func(t *testing.T, req *analyticspb.GetUserAnalyticsRequest, want *analyticsmodel.GetUserAnalyticsResponse) {
				t.Helper()

				var wg sync.WaitGroup
				results := make([]*analyticsmodel.GetUserAnalyticsResponse, 2)
				errs := make([]error, 2)

				for i := range 2 {
					idx := i
					wg.Go(func() {
						results[idx], errs[idx] = s.GetUserAnalytics(t.Context(), req)
					})
				}

				// Wait for the leader to park, then hold it long enough for the
				// second caller to join the flight instead of opening a second.
				select {
				case <-singleflightLeaderRan:
				case <-time.After(30 * time.Second):
					t.Fatal("timed out waiting for the singleflight leader to reach the repository")
				}
				time.Sleep(100 * time.Millisecond)
				close(singleflightReleaseChan)
				wg.Wait()

				assert.Equal(t, int32(1), atomic.LoadInt32(&singleflightRepoCalls))
				assert.NoError(t, errs[0])
				assert.NoError(t, errs[1])
				assert.Equal(t, want, results[0])
				assert.Equal(t, want, results[1])
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
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			if tt.execute != nil {
				tt.execute(t, tt.req, tt.want)
				return
			}

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
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
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
