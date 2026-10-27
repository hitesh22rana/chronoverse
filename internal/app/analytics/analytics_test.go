package analytics_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"

	"github.com/hitesh22rana/chronoverse/internal/app/analytics"
	analyticsmock "github.com/hitesh22rana/chronoverse/internal/app/analytics/mock"
	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
)

func initClient(t *testing.T, server *grpc.Server, transport credentials.TransportCredentials) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	done := make(chan error, 1)
	go func() { done <- server.Serve(lis) }()
	t.Cleanup(func() {
		server.Stop()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				t.Errorf("serve analytics: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("analytics server did not stop")
		}
	})
	conn, err := grpc.NewClient("passthrough:///analytics.test",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(transport),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return conn
}

func TestGetUserAnalytics(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := analyticsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client := analyticspb.NewAnalyticsServiceClient(initClient(t, analytics.New(t.Context(), &analytics.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc), insecure.NewCredentials()))

	type args struct {
		getCtx func() context.Context
		req    *analyticspb.GetUserAnalyticsRequest
	}

	tests := []struct {
		name     string
		args     args
		mock     func(*analyticspb.GetUserAnalyticsRequest)
		res      *analyticspb.GetUserAnalyticsResponse
		wantCode codes.Code
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						t.Context(),
						"token",
					)
				},
				req: &analyticspb.GetUserAnalyticsRequest{
					UserId: "user1",
				},
			},
			mock: func(_ *analyticspb.GetUserAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(
					gomock.Any(),
					gomock.Eq(auth.ServiceNameAnalytics),
				).Return(auth.WithAudience(auth.WithRole(context.Background(), string(auth.RoleUser)), "users-service"), &jwt.Token{}, nil)
				svc.EXPECT().GetUserAnalytics(
					gomock.Any(),
					gomock.Any(),
				).Return(&analyticsmodel.GetUserAnalyticsResponse{
					TotalWorkflows:            10,
					TotalJobs:                 100,
					TotalJoblogs:              1000,
					TotalJobExecutionDuration: 1000000,
				}, nil)
			},
			res: &analyticspb.GetUserAnalyticsResponse{
				TotalWorkflows:            10,
				TotalJobs:                 100,
				TotalJoblogs:              1000,
				TotalJobExecutionDuration: 1000000,
			},
			wantCode: codes.OK,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						t.Context(),
						"invalid-token",
					)
				},
				req: &analyticspb.GetUserAnalyticsRequest{
					UserId: "user1",
				},
			},
			mock: func(_ *analyticspb.GetUserAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any(), gomock.Any()).Return(auth.WithAudience(auth.WithRole(context.Background(), string(auth.RoleUser)), "users-service"), &jwt.Token{}, status.Error(codes.Unauthenticated, "invalid token")) //nolint:lll // line length due to mock setup
			},
			res:      nil,
			wantCode: codes.Unauthenticated,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						t.Context(),
						"token",
					)
				},
				req: &analyticspb.GetUserAnalyticsRequest{
					UserId: "",
				},
			},
			mock: func(_ *analyticspb.GetUserAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any(), gomock.Any()).Return(auth.WithAudience(auth.WithRole(context.Background(), string(auth.RoleUser)), "users-service"), &jwt.Token{}, nil)
				svc.EXPECT().GetUserAnalytics(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "missing required fields in request"))
			},
			res:      nil,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "error: missing required headers in metadata",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						t.Context(),
					)
				},
				req: &analyticspb.GetUserAnalyticsRequest{
					UserId: "user1",
				},
			},
			mock:     func(_ *analyticspb.GetUserAnalyticsRequest) {},
			res:      nil,
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						t.Context(),
						"token",
					)
				},
				req: &analyticspb.GetUserAnalyticsRequest{
					UserId: "user1",
				},
			},
			mock: func(_ *analyticspb.GetUserAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any(), gomock.Any()).Return(auth.WithAudience(auth.WithRole(context.Background(), string(auth.RoleUser)), "users-service"), &jwt.Token{}, nil)
				svc.EXPECT().GetUserAnalytics(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			res:      nil,
			wantCode: codes.Internal,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			res, err := client.GetUserAnalytics(tt.args.getCtx(), tt.args.req)
			require.Equal(t, tt.wantCode, status.Code(err), "%v", err)
			if tt.wantCode != codes.OK {
				require.Nil(t, res)
				return
			}
			require.NoError(t, err)
			if !proto.Equal(res, tt.res) {
				t.Errorf("response = %v, want %v", res, tt.res)
			}
		})
	}
}

func TestGetWorkflowAnalytics(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := analyticsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client := analyticspb.NewAnalyticsServiceClient(initClient(t, analytics.New(t.Context(), &analytics.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc), insecure.NewCredentials()))

	type args struct {
		getCtx func() context.Context
		req    *analyticspb.GetWorkflowAnalyticsRequest
	}

	tests := []struct {
		name     string
		args     args
		mock     func(*analyticspb.GetWorkflowAnalyticsRequest)
		res      *analyticspb.GetWorkflowAnalyticsResponse
		wantCode codes.Code
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						t.Context(),
						"token",
					)
				},
				req: &analyticspb.GetWorkflowAnalyticsRequest{
					UserId:     "user1",
					WorkflowId: "workflow_id",
				},
			},
			mock: func(_ *analyticspb.GetWorkflowAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any(), gomock.Any()).Return(auth.WithAudience(auth.WithRole(context.Background(), string(auth.RoleUser)), "users-service"), &jwt.Token{}, nil)
				svc.EXPECT().GetWorkflowAnalytics(
					gomock.Any(),
					gomock.Any(),
				).Return(&analyticsmodel.GetWorkflowAnalyticsResponse{
					WorkflowID:                "workflow_id",
					TotalJobExecutionDuration: 20000,
					TotalJobs:                 20,
					TotalJoblogs:              200,
				}, nil)
			},
			res: &analyticspb.GetWorkflowAnalyticsResponse{
				WorkflowId:                "workflow_id",
				TotalJobExecutionDuration: 20000,
				TotalJobs:                 20,
				TotalJoblogs:              200,
			},
			wantCode: codes.OK,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						t.Context(),
						"invalid-token",
					)
				},
				req: &analyticspb.GetWorkflowAnalyticsRequest{
					UserId:     "user1",
					WorkflowId: "workflow_id",
				},
			},
			mock: func(_ *analyticspb.GetWorkflowAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any(), gomock.Any()).Return(auth.WithAudience(auth.WithRole(context.Background(), string(auth.RoleUser)), "users-service"), &jwt.Token{}, status.Error(codes.Unauthenticated, "invalid token")) //nolint:lll // line length due to mock setup
			},
			res:      nil,
			wantCode: codes.Unauthenticated,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						t.Context(),
						"token",
					)
				},
				req: &analyticspb.GetWorkflowAnalyticsRequest{
					UserId:     "user1",
					WorkflowId: "",
				},
			},
			mock: func(_ *analyticspb.GetWorkflowAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any(), gomock.Any()).Return(auth.WithAudience(auth.WithRole(context.Background(), string(auth.RoleUser)), "users-service"), &jwt.Token{}, nil)
				svc.EXPECT().GetWorkflowAnalytics(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "missing required fields in request"))
			},
			res:      nil,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "error: missing required headers in metadata",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						t.Context(),
					)
				},
				req: &analyticspb.GetWorkflowAnalyticsRequest{
					UserId:     "user1",
					WorkflowId: "workflow_id",
				},
			},
			mock:     func(_ *analyticspb.GetWorkflowAnalyticsRequest) {},
			res:      nil,
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						t.Context(),
						"token",
					)
				},
				req: &analyticspb.GetWorkflowAnalyticsRequest{
					UserId:     "user1",
					WorkflowId: "workflow_id",
				},
			},
			mock: func(_ *analyticspb.GetWorkflowAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any(), gomock.Any()).Return(auth.WithAudience(auth.WithRole(context.Background(), string(auth.RoleUser)), "users-service"), &jwt.Token{}, nil)
				svc.EXPECT().GetWorkflowAnalytics(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			res:      nil,
			wantCode: codes.Internal,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			res, err := client.GetWorkflowAnalytics(tt.args.getCtx(), tt.args.req)
			require.Equal(t, tt.wantCode, status.Code(err), "%v", err)
			if tt.wantCode != codes.OK {
				require.Nil(t, res)
				return
			}
			require.NoError(t, err)
			if !proto.Equal(res, tt.res) {
				t.Errorf("response = %v, want %v", res, tt.res)
			}
		})
	}
}
