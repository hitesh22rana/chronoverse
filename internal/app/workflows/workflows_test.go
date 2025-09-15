package workflows_test

import (
	"context"
	"database/sql"
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

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	"github.com/hitesh22rana/chronoverse/internal/app/workflows"
	workflowsmock "github.com/hitesh22rana/chronoverse/internal/app/workflows/mock"
	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	server := workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc)

	_ = server
}

func initClient(server *grpc.Server) (client workflowspb.WorkflowsServiceClient, _close func()) {
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

	return workflowspb.NewWorkflowsServiceClient(conn), _close
}

func TestCreateWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.CreateWorkflowRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.CreateWorkflowRequest)
		res   *workflowspb.CreateWorkflowResponse
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
				req: &workflowspb.CreateWorkflowRequest{
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock: func(_ *workflowspb.CreateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().CreateWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return("workflow_id", nil)
			},
			res: &workflowspb.CreateWorkflowResponse{
				Id: "workflow_id",
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
								t.Context(), "internal-service",
							),
							auth.RoleUser,
						),
						"invalid-token",
					)
				},
				req: &workflowspb.CreateWorkflowRequest{
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock: func(_ *workflowspb.CreateWorkflowRequest) {
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
				req: &workflowspb.CreateWorkflowRequest{
					UserId:   "",
					Name:     "",
					Payload:  "",
					Kind:     "",
					Interval: 0,
				},
			},
			mock: func(_ *workflowspb.CreateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().CreateWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return("", status.Error(codes.InvalidArgument, "user_id, name, payload, kind, interval are required"))
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
				req: &workflowspb.CreateWorkflowRequest{
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock:  func(_ *workflowspb.CreateWorkflowRequest) {},
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
				req: &workflowspb.CreateWorkflowRequest{
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock: func(_ *workflowspb.CreateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().CreateWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return("", status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CreateWorkflow(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("CreateWorkflow() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("CreateWorkflow() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestUpdateWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.UpdateWorkflowRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.UpdateWorkflowRequest)
		res   *workflowspb.UpdateWorkflowResponse
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
				req: &workflowspb.UpdateWorkflowRequest{
					Id:                               "workflow_id",
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock: func(_ *workflowspb.UpdateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &workflowspb.UpdateWorkflowResponse{},
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
				req: &workflowspb.UpdateWorkflowRequest{
					Id:                               "workflow_id",
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock: func(_ *workflowspb.UpdateWorkflowRequest) {
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
				req: &workflowspb.UpdateWorkflowRequest{
					Id:       "",
					UserId:   "",
					Name:     "",
					Payload:  "",
					Interval: 0,
				},
			},
			mock: func(_ *workflowspb.UpdateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.InvalidArgument, "id, user_id, name, payload, kind, interval, and max_retry are required"))
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
				req: &workflowspb.UpdateWorkflowRequest{
					Id:                               "workflow_id",
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock:  func(_ *workflowspb.UpdateWorkflowRequest) {},
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
				req: &workflowspb.UpdateWorkflowRequest{
					Id:                               "workflow_id",
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock: func(_ *workflowspb.UpdateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.UpdateWorkflow(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("UpdateWorkflow() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("UpdateWorkflow() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestUpdateWorkflowBuildStatus(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.UpdateWorkflowBuildStatusRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.UpdateWorkflowBuildStatusRequest)
		res   *workflowspb.UpdateWorkflowBuildStatusResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.UpdateWorkflowBuildStatusRequest{
					Id:          "workflow_id",
					UserId:      "user1",
					BuildStatus: "COMPLETED",
				},
			},
			mock: func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateWorkflowBuildStatus(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &workflowspb.UpdateWorkflowBuildStatusResponse{},
			isErr: false,
		},
		{
			name: "error: unauthorized access (invalid role)",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &workflowspb.UpdateWorkflowBuildStatusRequest{
					Id:          "workflow_id",
					UserId:      "user1",
					BuildStatus: "COMPLETED",
				},
			},
			mock:  func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"invalid-token",
					)
				},
				req: &workflowspb.UpdateWorkflowBuildStatusRequest{
					Id:          "workflow_id",
					UserId:      "user1",
					BuildStatus: "COMPLETED",
				},
			},
			mock: func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {
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
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.UpdateWorkflowBuildStatusRequest{
					Id:          "",
					UserId:      "",
					BuildStatus: "",
				},
			},
			mock: func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateWorkflowBuildStatus(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.InvalidArgument, "id and build_status are required"))
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
				req: &workflowspb.UpdateWorkflowBuildStatusRequest{
					Id:          "workflow_id",
					UserId:      "user1",
					BuildStatus: "COMPLETED",
				},
			},
			mock:  func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {},
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
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.UpdateWorkflowBuildStatusRequest{
					Id:          "workflow_id",
					UserId:      "user1",
					BuildStatus: "COMPLETED",
				},
			},
			mock: func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateWorkflowBuildStatus(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.UpdateWorkflowBuildStatus(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("UpdateWorkflowBuildStatus() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("UpdateWorkflowBuildStatus() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestGetWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.GetWorkflowRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.GetWorkflowRequest)
		res   *workflowspb.GetWorkflowResponse
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
				req: &workflowspb.GetWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.GetWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(&workflowsmodel.GetWorkflowResponse{
					ID:                               "workflow_id",
					Name:                             "job1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					WorkflowBuildStatus:              "COMPLETED",
					Interval:                         1,
					ConsecutiveJobFailuresCount:      0,
					MaxConsecutiveJobFailuresAllowed: 5,
					CreatedAt:                        time.Now(),
					UpdatedAt:                        time.Now(),
					TerminatedAt: sql.NullTime{
						Time:  time.Now(),
						Valid: true,
					},
				}, nil)
			},
			res: &workflowspb.GetWorkflowResponse{
				Id:                               "workflow_id",
				Name:                             "job1",
				Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
				Kind:                             "HEARTBEAT",
				BuildStatus:                      "COMPLETED",
				Interval:                         1,
				ConsecutiveJobFailuresCount:      0,
				MaxConsecutiveJobFailuresAllowed: 5,
				CreatedAt:                        time.Now().Format(time.RFC3339Nano),
				UpdatedAt:                        time.Now().Format(time.RFC3339Nano),
				TerminatedAt:                     time.Now().Format(time.RFC3339Nano),
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
				req: &workflowspb.GetWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.GetWorkflowRequest) {
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
				req: &workflowspb.GetWorkflowRequest{
					Id:     "",
					UserId: "",
				},
			},
			mock: func(_ *workflowspb.GetWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "user_id and name are required"))
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
				req: &workflowspb.GetWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock:  func(_ *workflowspb.GetWorkflowRequest) {},
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
				req: &workflowspb.GetWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.GetWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetWorkflow(
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
			_, err := client.GetWorkflow(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("GetWorkflow() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("GetWorkflow() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestGetWorkflowByID(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.GetWorkflowByIDRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.GetWorkflowByIDRequest)
		res   *workflowspb.GetWorkflowByIDResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.GetWorkflowByIDRequest{
					Id: "workflow_id",
				},
			},
			mock: func(_ *workflowspb.GetWorkflowByIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetWorkflowByID(
					gomock.Any(),
					gomock.Any(),
				).Return(&workflowsmodel.GetWorkflowByIDResponse{
					ID:                               "workflow_id",
					UserID:                           "user1",
					Name:                             "job1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					WorkflowBuildStatus:              "COMPLETED",
					Interval:                         1,
					ConsecutiveJobFailuresCount:      0,
					MaxConsecutiveJobFailuresAllowed: 5,
					CreatedAt:                        time.Now(),
					UpdatedAt:                        time.Now(),
					TerminatedAt: sql.NullTime{
						Time:  time.Now(),
						Valid: true,
					},
				}, nil)
			},
			res: &workflowspb.GetWorkflowByIDResponse{
				Id:                               "workflow_id",
				UserId:                           "user1",
				Name:                             "job1",
				Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
				Kind:                             "HEARTBEAT",
				BuildStatus:                      "COMPLETED",
				Interval:                         1,
				ConsecutiveJobFailuresCount:      0,
				MaxConsecutiveJobFailuresAllowed: 5,
				CreatedAt:                        time.Now().Format(time.RFC3339Nano),
				UpdatedAt:                        time.Now().Format(time.RFC3339Nano),
				TerminatedAt:                     time.Now().Format(time.RFC3339Nano),
			},
			isErr: false,
		},
		{
			name: "error: unauthorized access (invalid role)",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &workflowspb.GetWorkflowByIDRequest{
					Id: "workflow_id",
				},
			},
			mock:  func(_ *workflowspb.GetWorkflowByIDRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"invalid-token",
					)
				},
				req: &workflowspb.GetWorkflowByIDRequest{
					Id: "workflow_id",
				},
			},
			mock: func(_ *workflowspb.GetWorkflowByIDRequest) {
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
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.GetWorkflowByIDRequest{
					Id: "",
				},
			},
			mock: func(_ *workflowspb.GetWorkflowByIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetWorkflowByID(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "id is required"))
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
				req: &workflowspb.GetWorkflowByIDRequest{
					Id: "workflow_id",
				},
			},
			mock:  func(_ *workflowspb.GetWorkflowByIDRequest) {},
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
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.GetWorkflowByIDRequest{
					Id: "workflow_id",
				},
			},
			mock: func(_ *workflowspb.GetWorkflowByIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetWorkflowByID(
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
			_, err := client.GetWorkflowByID(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("GetWorkflowByID() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("GetWorkflowByID() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestIncrementWorkflowConsecutiveJobFailuresCount(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest)
		res   *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse
		isErr bool
	}{
		{
			name: "success: threshold not reached",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().IncrementWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					gomock.Any(),
				).Return(false, nil)
			},
			res: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse{
				ThresholdReached: false,
			},
			isErr: false,
		},
		{
			name: "success: threshold reached",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().IncrementWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					gomock.Any(),
				).Return(true, nil)
			},
			res: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse{
				ThresholdReached: true,
			},
			isErr: false,
		},
		{
			name: "error: unauthorized access (invalid role)",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock:  func(_ *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"invalid-token",
					)
				},
				req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {
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
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().IncrementWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					gomock.Any(),
				).Return(false, status.Error(codes.InvalidArgument, "id is required"))
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
				req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock:  func(_ *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {},
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
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().IncrementWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					gomock.Any(),
				).Return(false, status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.IncrementWorkflowConsecutiveJobFailuresCount(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("IncrementWorkflowConsecutiveJobFailuresCount() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("IncrementWorkflowConsecutiveJobFailuresCount() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestResetWorkflowConsecutiveJobFailuresCount(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest)
		res   *workflowspb.ResetWorkflowConsecutiveJobFailuresCountResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ResetWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &workflowspb.ResetWorkflowConsecutiveJobFailuresCountResponse{},
			isErr: false,
		},
		{
			name: "error: unauthorized access (invalid role)",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock:  func(_ *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"invalid-token",
					)
				},
				req: &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) {
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
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
					Id:     "",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ResetWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.InvalidArgument, "id is required"))
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
				req: &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
					Id: "workflow_id",
				},
			},
			mock:  func(_ *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) {},
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
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
					Id: "workflow_id",
				},
			},
			mock: func(_ *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ResetWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.ResetWorkflowConsecutiveJobFailuresCount(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("ResetWorkflowConsecutiveJobFailuresCount() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("ResetWorkflowConsecutiveJobFailuresCount() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestTerminateWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.TerminateWorkflowRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.TerminateWorkflowRequest)
		res   *workflowspb.TerminateWorkflowResponse
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
				req: &workflowspb.TerminateWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.TerminateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().TerminateWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &workflowspb.TerminateWorkflowResponse{},
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
				req: &workflowspb.TerminateWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.TerminateWorkflowRequest) {
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
				req: &workflowspb.TerminateWorkflowRequest{
					Id:     "",
					UserId: "",
				},
			},
			mock: func(_ *workflowspb.TerminateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().TerminateWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.InvalidArgument, "id and user_id are required"))
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
				req: &workflowspb.TerminateWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock:  func(_ *workflowspb.TerminateWorkflowRequest) {},
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
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.TerminateWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.TerminateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().TerminateWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.TerminateWorkflow(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("TerminateWorkflow() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("TerminateWorkflow() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestDeleteWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.DeleteWorkflowRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.DeleteWorkflowRequest)
		res   *workflowspb.DeleteWorkflowResponse
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
				req: &workflowspb.DeleteWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.DeleteWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().DeleteWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &workflowspb.DeleteWorkflowResponse{},
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
				req: &workflowspb.DeleteWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.DeleteWorkflowRequest) {
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
				req: &workflowspb.DeleteWorkflowRequest{
					Id:     "",
					UserId: "",
				},
			},
			mock: func(_ *workflowspb.DeleteWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().DeleteWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.InvalidArgument, "id and user_id are required"))
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
				req: &workflowspb.DeleteWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock:  func(_ *workflowspb.DeleteWorkflowRequest) {},
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
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &workflowspb.DeleteWorkflowRequest{
					Id:     "workflow_id",
					UserId: "user1",
				},
			},
			mock: func(_ *workflowspb.DeleteWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().DeleteWorkflow(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.DeleteWorkflow(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("DeleteWorkflow() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("DeleteWorkflow() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestListWorkflows(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.ListWorkflowsRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.ListWorkflowsRequest)
		res   *workflowspb.ListWorkflowsResponse
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
				req: &workflowspb.ListWorkflowsRequest{
					UserId: "user1",
					Cursor: "",
				},
			},
			mock: func(_ *workflowspb.ListWorkflowsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListWorkflows(
					gomock.Any(),
					gomock.Any(),
				).Return(&workflowsmodel.ListWorkflowsResponse{
					Workflows: []*workflowsmodel.WorkflowByUserIDResponse{
						{
							ID:                               "workflow_id",
							Name:                             "job1",
							Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
							Kind:                             "HEARTBEAT",
							WorkflowBuildStatus:              "COMPLETED",
							Interval:                         1,
							ConsecutiveJobFailuresCount:      0,
							MaxConsecutiveJobFailuresAllowed: 5,
							CreatedAt:                        time.Now(),
							UpdatedAt:                        time.Now(),
							TerminatedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: true,
							},
						},
					},
					Cursor: "",
				}, nil)
			},
			res: &workflowspb.ListWorkflowsResponse{
				Workflows: []*workflowspb.WorkflowsByUserIDResponse{
					{
						Id:                               "workflow_id",
						Name:                             "job1",
						Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
						Kind:                             "HEARTBEAT",
						BuildStatus:                      "COMPLETED",
						Interval:                         1,
						ConsecutiveJobFailuresCount:      0,
						MaxConsecutiveJobFailuresAllowed: 5,
						CreatedAt:                        time.Now().Format(time.RFC3339Nano),
						UpdatedAt:                        time.Now().Format(time.RFC3339Nano),
						TerminatedAt:                     time.Now().Format(time.RFC3339Nano),
					},
				},
				Cursor: "",
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
								t.Context(), "internal-service",
							),
							auth.RoleUser,
						),
						"invalid-token",
					)
				},
				req: &workflowspb.ListWorkflowsRequest{
					UserId: "user1",
					Cursor: "",
				},
			},
			mock: func(_ *workflowspb.ListWorkflowsRequest) {
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
				req: &workflowspb.ListWorkflowsRequest{
					UserId: "",
					Cursor: "",
				},
			},
			mock: func(_ *workflowspb.ListWorkflowsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListWorkflows(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "user_id is required"))
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
				req: &workflowspb.ListWorkflowsRequest{
					UserId: "user1",
					Cursor: "",
				},
			},
			mock:  func(_ *workflowspb.ListWorkflowsRequest) {},
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
				req: &workflowspb.ListWorkflowsRequest{
					UserId: "user1",
					Cursor: "",
				},
			},
			mock: func(_ *workflowspb.ListWorkflowsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListWorkflows(
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
			_, err := client.ListWorkflows(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("ListWorkflows() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("ListWorkflows() error = nil, want error")
				}
				return
			}
		})
	}
}

//nolint:gocyclo // This function is complex due to the nature of streaming logs and context cancellation.
func TestStreamTestWorkflowRun(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(workflows.New(t.Context(), &workflows.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *workflowspb.CreateWorkflowRequest
	}

	type want struct {
		messageCount int
		logCount     int
		expectClosed bool
	}

	// Test cases
	tests := []struct {
		name  string
		args  args
		mock  func(*workflowspb.CreateWorkflowRequest)
		want  want
		isErr bool
	}{
		{
			name: "success: stream logs",
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
				req: &workflowspb.CreateWorkflowRequest{
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"image": "alpine:latest","cmd": ["sh", "-c", 'for i in $(seq 1 20); do echo "hello world $i";sleep 1; done'], "timeout": "25s"}`,
					Kind:                             "CONTAINER",
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock: func(_ *workflowspb.CreateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)

				// Create a channel that will send response then close
				ch := make(chan *workflowsmodel.StreamTestWorkflowRunResponse, 3)
				go func() {
					defer close(ch)
					ch <- &workflowsmodel.StreamTestWorkflowRunResponse{
						Status:  workflowsmodel.TestWorkflowRunStatusBuilding,
						Message: "Building",
					}
					ch <- &workflowsmodel.StreamTestWorkflowRunResponse{
						Status: workflowsmodel.TestWorkflowRunStatusRunning,
						Kind:   workflowsmodel.KindContainer,
						Log: &jobsmodel.JobLog{
							Timestamp:   time.Now(),
							Message:     "Log message 1",
							SequenceNum: 1,
						},
					}
					ch <- &workflowsmodel.StreamTestWorkflowRunResponse{
						Status: workflowsmodel.TestWorkflowRunStatusRunning,
						Kind:   workflowsmodel.KindContainer,
						Log: &jobsmodel.JobLog{
							Timestamp:   time.Now(),
							Message:     "Log message 2",
							SequenceNum: 2,
						},
					}
					ch <- &workflowsmodel.StreamTestWorkflowRunResponse{
						Status:  workflowsmodel.TestWorkflowRunStatusCompleted,
						Message: "Completed",
					}
				}()

				svc.EXPECT().StreamTestWorkflowRun(
					gomock.Any(),
					gomock.Any(),
				).Return(ch, nil)
			},
			want: want{
				messageCount: 2,
				logCount:     2,
				expectClosed: true,
			},
			isErr: false,
		},
		{
			name: "success: context cancellation",
			args: args{
				getCtx: func() context.Context {
					ctx := auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
					ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
					// Cancel after a short delay to test context cancellation
					go func() {
						time.Sleep(50 * time.Millisecond)
						cancel()
					}()
					return ctx
				},
				req: &workflowspb.CreateWorkflowRequest{
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"image": "alpine:latest","cmd": ["sh", "-c", 'for i in $(seq 1 20); do echo "hello world $i";sleep 1; done'], "timeout": "25s"}`,
					Kind:                             "CONTAINER",
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock: func(_ *workflowspb.CreateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)

				// Create a channel that sends logs continuously
				ch := make(chan *workflowsmodel.StreamTestWorkflowRunResponse)
				go func() {
					defer close(ch)
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-ticker.C:
						case <-time.After(200 * time.Millisecond):
							return // Exit after timeout
						}
					}
				}()

				svc.EXPECT().StreamTestWorkflowRun(
					gomock.Any(),
					gomock.Any(),
				).Return(ch, nil)
			},
			// We don't expect any specific count due to context cancellation
			want: want{
				messageCount: 0,
				logCount:     0,
				expectClosed: false,
			},
			isErr: false, // Context cancellation should not return an error from the gRPC method
		},
		{
			name: "error: missing required headers in metadata",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						t.Context(),
					)
				},
				req: &workflowspb.CreateWorkflowRequest{
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"image": "alpine:latest","cmd": ["sh", "-c", 'for i in $(seq 1 20); do echo "hello world $i";sleep 1; done'], "timeout": "25s"}`,
					Kind:                             "CONTAINER",
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock: func(_ *workflowspb.CreateWorkflowRequest) {},
			want: want{
				logCount:     0,
				expectClosed: false,
			},
			isErr: true,
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
				req: &workflowspb.CreateWorkflowRequest{
					UserId:                           "user1",
					Name:                             "job1",
					Payload:                          `{"image": "alpine:latest","cmd": ["sh", "-c", 'for i in $(seq 1 20); do echo "hello world $i";sleep 1; done'], "timeout": "25s"}`,
					Kind:                             "CONTAINER",
					Interval:                         1,
					MaxConsecutiveJobFailuresAllowed: 5,
				},
			},
			mock: func(_ *workflowspb.CreateWorkflowRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(nil, status.Error(codes.Unauthenticated, "invalid token"))
			},
			want: want{
				logCount:     0,
				expectClosed: false,
			},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock(tt.args.req)
			stream, err := client.StreamTestWorkflowRun(tt.args.getCtx(), tt.args.req)
			if err != nil && tt.isErr {
				return
			}

			if tt.isErr {
				if stream == nil {
					t.Error("StreamTestWorkflowRun() returned nil stream, want error")
					return
				}

				_, err = stream.Recv()
				if err == nil {
					t.Error("StreamTestWorkflowRun() error = nil, want error")
				}
				return
			}

			if err != nil {
				t.Errorf("StreamTestWorkflowRun() error = %v, want nil", err)
				return
			}

			if stream == nil {
				t.Errorf("StreamTestWorkflowRun() stream = nil, want stream")
				return
			}

			// Read logs from the stream
			var (
				logCount     int
				messageCount int
				streamClosed bool
			)

			// Timeout to prevent test from hanging
			timeout := time.After(300 * time.Millisecond)
			for {
				select {
				case <-timeout:
					// Test timeout - exit the loop
					goto done
				default:
					data, err := stream.Recv()
					if err != nil {
						// Stream ended or error occurred
						streamClosed = true
						goto done
					}
					switch v := data.Data.(type) {
					case *workflowspb.StreamTestWorkflowRunResponse_Log:
						log := v.Log
						if log != nil {
							logCount++
							if log.Message == "" {
								t.Errorf("received log with empty message")
							}
							if log.SequenceNum == 0 {
								t.Errorf("received log with zero sequence number")
							}
							if log.Timestamp == "" {
								t.Errorf("received log with empty timestamp")
							}
						}
					case *workflowspb.StreamTestWorkflowRunResponse_Message:
						messageCount++
					default:
						// Unknown type
					}
				}
			}

		done:
			// Verify expectations based on test case
			if tt.want.expectClosed && !streamClosed {
				t.Errorf("expected stream to be closed, but it's still open")
			}

			if messageCount != tt.want.messageCount {
				t.Errorf("expected %d message, got %d", tt.want.messageCount, messageCount)
			}

			if tt.want.logCount > 0 && logCount != tt.want.logCount {
				t.Errorf("expected %d logs, got %d", tt.want.logCount, logCount)
			}

			// Ensures stream is properly closed
			if !streamClosed {
				if err := stream.CloseSend(); err != nil {
					t.Errorf("failed to close stream: %v", err)
				}
			}
		})
	}
}
