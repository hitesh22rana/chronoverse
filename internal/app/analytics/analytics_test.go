package analytics_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"

	"github.com/hitesh22rana/chronoverse/internal/app/analytics"
	analyticsmock "github.com/hitesh22rana/chronoverse/internal/app/analytics/mock"
	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := analyticsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	server := analytics.New(t.Context(), &analytics.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc)

	_ = server
}

func initClient(server *grpc.Server) (client analyticspb.AnalyticsServiceClient, _close func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	buffer := 1024 * 1024
	lis := bufconn.Listen(buffer)

	go func() {
		if err := server.Serve(lis); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to serve gRPC server: %v\n", err)
		}
	}()

	//nolint:staticcheck // SA1019: This is required for testing.
	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to gRPC server: %v\n", err)
		return nil, nil
	}

	_close = func() {
		err := lis.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to close listener: %v\n", err)
		}

		server.Stop()
	}

	return analyticspb.NewAnalyticsServiceClient(conn), _close
}

func TestGetUserAnalytics(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := analyticsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(analytics.New(t.Context(), &analytics.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *analyticspb.GetUserAnalyticsRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*analyticspb.GetUserAnalyticsRequest)
		res   *analyticspb.GetUserAnalyticsResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &analyticspb.GetUserAnalyticsRequest{
					UserId: "user1",
				},
			},
			mock: func(_ *analyticspb.GetUserAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
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
			isErr: false,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"invalid-token",
					)
				},
				req: &analyticspb.GetUserAnalyticsRequest{
					UserId: "user1",
				},
			},
			mock: func(_ *analyticspb.GetUserAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, status.Error(codes.Unauthenticated, "invalid token"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &analyticspb.GetUserAnalyticsRequest{
					UserId: "",
				},
			},
			mock: func(_ *analyticspb.GetUserAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetUserAnalytics(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "missing required fields in request"))
			},
			res:   nil,
			isErr: true,
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
			mock:  func(_ *analyticspb.GetUserAnalyticsRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &analyticspb.GetUserAnalyticsRequest{
					UserId: "user1",
				},
			},
			mock: func(_ *analyticspb.GetUserAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetUserAnalytics(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.GetUserAnalytics(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("GetUserAnalytics() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("GetUserAnalytics() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestGetWorkflowAnalytics(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := analyticsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(analytics.New(t.Context(), &analytics.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *analyticspb.GetWorkflowAnalyticsRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*analyticspb.GetWorkflowAnalyticsRequest)
		res   *analyticspb.GetWorkflowAnalyticsResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &analyticspb.GetWorkflowAnalyticsRequest{
					UserId:     "user1",
					WorkflowId: "workflow_id",
				},
			},
			mock: func(_ *analyticspb.GetWorkflowAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
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
			isErr: false,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"invalid-token",
					)
				},
				req: &analyticspb.GetWorkflowAnalyticsRequest{
					UserId:     "user1",
					WorkflowId: "workflow_id",
				},
			},
			mock: func(_ *analyticspb.GetWorkflowAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, status.Error(codes.Unauthenticated, "invalid token"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &analyticspb.GetWorkflowAnalyticsRequest{
					UserId:     "user1",
					WorkflowId: "",
				},
			},
			mock: func(_ *analyticspb.GetWorkflowAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetWorkflowAnalytics(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "missing required fields in request"))
			},
			res:   nil,
			isErr: true,
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
			mock:  func(_ *analyticspb.GetWorkflowAnalyticsRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &analyticspb.GetWorkflowAnalyticsRequest{
					UserId:     "user1",
					WorkflowId: "workflow_id",
				},
			},
			mock: func(_ *analyticspb.GetWorkflowAnalyticsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetWorkflowAnalytics(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.GetWorkflowAnalytics(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("GetWorkflowAnalytics() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("GetWorkflowAnalytics() error = nil, want error")
				}
				return
			}
		})
	}
}
