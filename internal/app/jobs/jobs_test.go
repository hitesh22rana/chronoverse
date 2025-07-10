package jobs_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/golang-jwt/jwt/v5"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	"github.com/hitesh22rana/chronoverse/internal/app/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/app/jobs/mock"
	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	server := jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc)

	_ = server
}

func initClient(server *grpc.Server) (client jobspb.JobsServiceClient, _close func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	buffer := 1024 * 1024
	lis := bufconn.Listen(buffer)

	go func() {
		if err := server.Serve(lis); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to serve gRPC server: %v\n", err)
		}
	}()

	//nolint:staticcheck // This is required for testing.
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

	return jobspb.NewJobsServiceClient(conn), _close
}

func TestScheduleJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.ScheduleJobRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.ScheduleJobRequest)
		res   *jobspb.ScheduleJobResponse
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
				req: &jobspb.ScheduleJobRequest{
					WorkflowId:  "workflow_id",
					UserId:      "user1",
					ScheduledAt: time.Now().Format(time.RFC3339Nano),
				},
			},
			mock: func(_ *jobspb.ScheduleJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ScheduleJob(
					gomock.Any(),
					gomock.Any(),
				).Return("job_id", nil)
			},
			res: &jobspb.ScheduleJobResponse{
				Id: "job_id",
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
				req: &jobspb.ScheduleJobRequest{
					WorkflowId:  "workflow_id",
					UserId:      "user1",
					ScheduledAt: time.Now().Format(time.RFC3339Nano),
				},
			},
			mock:  func(_ *jobspb.ScheduleJobRequest) {},
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
				req: &jobspb.ScheduleJobRequest{
					WorkflowId:  "workflow_id",
					UserId:      "user1",
					ScheduledAt: time.Now().Format(time.RFC3339Nano),
				},
			},
			mock: func(_ *jobspb.ScheduleJobRequest) {
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
				req: &jobspb.ScheduleJobRequest{
					WorkflowId:  "workflow_id",
					UserId:      "",
					ScheduledAt: "",
				},
			},
			mock: func(_ *jobspb.ScheduleJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ScheduleJob(
					gomock.Any(),
					gomock.Any(),
				).Return("", status.Error(codes.InvalidArgument, "job_id, user_id, and scheduled_at are required"))
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
				req: &jobspb.ScheduleJobRequest{
					WorkflowId:  "workflow_id",
					UserId:      "user1",
					ScheduledAt: time.Now().Format(time.RFC3339Nano),
				},
			},
			mock:  func(_ *jobspb.ScheduleJobRequest) {},
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
				req: &jobspb.ScheduleJobRequest{
					WorkflowId:  "workflow_id",
					UserId:      "user1",
					ScheduledAt: time.Now().Format(time.RFC3339Nano),
				},
			},
			mock: func(_ *jobspb.ScheduleJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ScheduleJob(
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
			_, err := client.ScheduleJob(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("ScheduleJob() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("ScheduleJob() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestUpdateJobStatus(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.UpdateJobStatusRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.UpdateJobStatusRequest)
		res   *jobspb.UpdateJobStatusResponse
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
				req: &jobspb.UpdateJobStatusRequest{
					Id:     "job_id",
					Status: "QUEUED",
				},
			},
			mock: func(_ *jobspb.UpdateJobStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateJobStatus(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &jobspb.UpdateJobStatusResponse{},
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
				req: &jobspb.UpdateJobStatusRequest{
					Id:     "job_id",
					Status: "QUEUED",
				},
			},
			mock:  func(_ *jobspb.UpdateJobStatusRequest) {},
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
				req: &jobspb.UpdateJobStatusRequest{
					Id:     "job_id",
					Status: "QUEUED",
				},
			},
			mock: func(_ *jobspb.UpdateJobStatusRequest) {
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
				req: &jobspb.UpdateJobStatusRequest{
					Id:     "",
					Status: "",
				},
			},
			mock: func(_ *jobspb.UpdateJobStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateJobStatus(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.InvalidArgument, "id and status are required"))
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
				req: &jobspb.UpdateJobStatusRequest{
					Id:     "job_id",
					Status: "QUEUED",
				},
			},
			mock:  func(_ *jobspb.UpdateJobStatusRequest) {},
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
				req: &jobspb.UpdateJobStatusRequest{
					Id:     "job_id",
					Status: "QUEUED",
				},
			},
			mock: func(_ *jobspb.UpdateJobStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateJobStatus(
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
			_, err := client.UpdateJobStatus(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("UpdateJobStatus() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("UpdateJobStatus() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestGetJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.GetJobRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.GetJobRequest)
		res   *jobspb.GetJobResponse
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
				req: &jobspb.GetJobRequest{
					UserId: "user1",
				},
			},
			mock: func(_ *jobspb.GetJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJob(
					gomock.Any(),
					gomock.Any(),
				).Return(&jobsmodel.GetJobResponse{
					ID:          "job_id",
					WorkflowID:  "workflow_id",
					JobStatus:   "QUEUED",
					ScheduledAt: time.Now(),
					StartedAt: sql.NullTime{
						Time:  time.Now(),
						Valid: true,
					},
					CompletedAt: sql.NullTime{
						Time:  time.Now(),
						Valid: true,
					},
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}, nil)
			},
			res: &jobspb.GetJobResponse{
				Id:          "job_id",
				WorkflowId:  "workflow_id",
				Status:      "PENDING",
				ScheduledAt: time.Now().Format(time.RFC3339Nano),
				StartedAt:   time.Now().Format(time.RFC3339Nano),
				CompletedAt: time.Now().Format(time.RFC3339Nano),
				CreatedAt:   time.Now().Format(time.RFC3339Nano),
				UpdatedAt:   time.Now().Format(time.RFC3339Nano),
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
				req: &jobspb.GetJobRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user1",
				},
			},
			mock: func(_ *jobspb.GetJobRequest) {
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
				req: &jobspb.GetJobRequest{
					Id:         "",
					WorkflowId: "",
					UserId:     "",
				},
			},
			mock: func(_ *jobspb.GetJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJob(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "job_id, workflow_id and user_id are required"))
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
				req: &jobspb.GetJobRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user1",
				},
			},
			mock:  func(_ *jobspb.GetJobRequest) {},
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
				req: &jobspb.GetJobRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user1",
				},
			},
			mock: func(_ *jobspb.GetJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJob(
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
			_, err := client.GetJob(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("GetJob() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("GetJob() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestGetJobByID(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.GetJobByIDRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.GetJobByIDRequest)
		res   *jobspb.GetJobByIDResponse
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
				req: &jobspb.GetJobByIDRequest{
					Id: "job_id",
				},
			},
			mock: func(_ *jobspb.GetJobByIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJobByID(
					gomock.Any(),
					gomock.Any(),
				).Return(&jobsmodel.GetJobByIDResponse{
					ID:          "job_id",
					WorkflowID:  "workflow_id",
					UserID:      "user1",
					JobStatus:   "QUEUED",
					ScheduledAt: time.Now(),
					StartedAt: sql.NullTime{
						Time:  time.Now(),
						Valid: true,
					},
					CompletedAt: sql.NullTime{
						Time:  time.Now(),
						Valid: true,
					},
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}, nil)
			},
			res: &jobspb.GetJobByIDResponse{
				Id:          "job_id",
				WorkflowId:  "workflow_id",
				UserId:      "user1",
				Status:      "PENDING",
				ScheduledAt: time.Now().Format(time.RFC3339Nano),
				StartedAt:   time.Now().Format(time.RFC3339Nano),
				CompletedAt: time.Now().Format(time.RFC3339Nano),
				CreatedAt:   time.Now().Format(time.RFC3339Nano),
				UpdatedAt:   time.Now().Format(time.RFC3339Nano),
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
				req: &jobspb.GetJobByIDRequest{
					Id: "job_id",
				},
			},
			mock:  func(_ *jobspb.GetJobByIDRequest) {},
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
				req: &jobspb.GetJobByIDRequest{
					Id: "job_id",
				},
			},
			mock: func(_ *jobspb.GetJobByIDRequest) {
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
				req: &jobspb.GetJobByIDRequest{
					Id: "",
				},
			},
			mock: func(_ *jobspb.GetJobByIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJobByID(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "job_id is required"))
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
				req: &jobspb.GetJobByIDRequest{
					Id: "job_id",
				},
			},
			mock:  func(_ *jobspb.GetJobByIDRequest) {},
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
				req: &jobspb.GetJobByIDRequest{
					Id: "job_id",
				},
			},
			mock: func(_ *jobspb.GetJobByIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJobByID(
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
			_, err := client.GetJobByID(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("GetJobByID() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("GetJobByID() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestGetJobLogs(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.GetJobLogsRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.GetJobLogsRequest)
		res   *jobspb.GetJobLogsResponse
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
				req: &jobspb.GetJobLogsRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user1",
					Cursor:     "",
				},
			},
			mock: func(_ *jobspb.GetJobLogsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJobLogs(
					gomock.Any(),
					gomock.Any(),
				).Return(&jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   time.Now(),
							Message:     "message 1",
							SequenceNum: 1,
						},
						{
							Timestamp:   time.Now(),
							Message:     "message 2",
							SequenceNum: 2,
						},
					},
				}, nil)
			},
			res: &jobspb.GetJobLogsResponse{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				Logs: []*jobspb.Log{
					{
						Timestamp:   time.Now().Format(time.RFC3339),
						Message:     "message 1",
						SequenceNum: 1,
					},
					{
						Timestamp:   time.Now().Format(time.RFC3339),
						Message:     "message 2",
						SequenceNum: 2,
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
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"invalid-token",
					)
				},
				req: &jobspb.GetJobLogsRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user1",
					Cursor:     "",
				},
			},
			mock: func(_ *jobspb.GetJobLogsRequest) {
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
				req: &jobspb.GetJobLogsRequest{
					Id:         "",
					WorkflowId: "",
					UserId:     "",
					Cursor:     "",
				},
			},
			mock: func(_ *jobspb.GetJobLogsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJobLogs(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "job_id, workflow_id and user_id are required"))
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
				req: &jobspb.GetJobLogsRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user1",
					Cursor:     "",
				},
			},
			mock:  func(_ *jobspb.GetJobLogsRequest) {},
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
				req: &jobspb.GetJobLogsRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user1",
					Cursor:     "",
				},
			},
			mock: func(_ *jobspb.GetJobLogsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJobLogs(
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
			_, err := client.GetJobLogs(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("GetJobLogs() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("GetJobLogs() error = nil, want error")
				}
				return
			}
		})
	}
}

// TestStreamJobLogs tests the StreamJobLogs method of the jobs service.
//
//nolint:gocyclo // This function is complex due to the nature of streaming logs and context cancellation.
func TestStreamJobLogs(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	server := jobs.New(t.Context(), &jobs.Config{}, _auth, svc)

	client, _close := initClient(server)
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.StreamJobLogsRequest
	}

	type want struct {
		logCount     int
		expectClosed bool
	}

	// Test cases
	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.StreamJobLogsRequest)
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
				req: &jobspb.StreamJobLogsRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user_id",
				},
			},
			mock: func(_ *jobspb.StreamJobLogsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)

				// Create a channel that will send a few logs then close
				ch := make(chan *jobsmodel.JobLog, 3)
				go func() {
					defer close(ch)
					ch <- &jobsmodel.JobLog{
						Timestamp:   time.Now(),
						Message:     "Log message 1",
						SequenceNum: 1,
					}
					ch <- &jobsmodel.JobLog{
						Timestamp:   time.Now(),
						Message:     "Log message 2",
						SequenceNum: 2,
					}
					ch <- &jobsmodel.JobLog{
						Timestamp:   time.Now(),
						Message:     "Log message 3",
						SequenceNum: 3,
					}
				}()

				svc.EXPECT().StreamJobLogs(
					gomock.Any(),
					gomock.Any(),
				).Return(ch, nil)
			},
			want: want{
				logCount:     3,
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
				req: &jobspb.StreamJobLogsRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user_id",
				},
			},
			mock: func(_ *jobspb.StreamJobLogsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)

				// Create a channel that sends logs continuously
				ch := make(chan *jobsmodel.JobLog)
				go func() {
					defer close(ch)
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					var i uint32 = 1
					for {
						select {
						case <-ticker.C:
							ch <- &jobsmodel.JobLog{
								Timestamp:   time.Now(),
								Message:     fmt.Sprintf("Log message %d", i),
								SequenceNum: i,
							}
							i++
						case <-time.After(200 * time.Millisecond):
							return // Exit after timeout
						}
					}
				}()

				svc.EXPECT().StreamJobLogs(
					gomock.Any(),
					gomock.Any(),
				).Return(ch, nil)
			},
			want: want{
				logCount:     0, // We don't expect any specific count due to context cancellation
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
				req: &jobspb.StreamJobLogsRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user_id",
				},
			},
			mock: func(_ *jobspb.StreamJobLogsRequest) {},
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
				req: &jobspb.StreamJobLogsRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user_id",
				},
			},
			mock: func(_ *jobspb.StreamJobLogsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(nil, status.Error(codes.Unauthenticated, "invalid token"))
			},
			want: want{
				logCount:     0,
				expectClosed: false,
			},
			isErr: true,
		},
		{
			name: "error: job not found",
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
				req: &jobspb.StreamJobLogsRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user_id",
				},
			},
			mock: func(_ *jobspb.StreamJobLogsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().StreamJobLogs(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
			},
			want: want{
				logCount:     0,
				expectClosed: false,
			},
			isErr: true,
		},
		{
			name: "error: job not running",
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
				req: &jobspb.StreamJobLogsRequest{
					Id:         "job_id",
					WorkflowId: "workflow_id",
					UserId:     "user_id",
				},
			},
			mock: func(_ *jobspb.StreamJobLogsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().StreamJobLogs(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.FailedPrecondition, "job is not running"))
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
			stream, err := client.StreamJobLogs(tt.args.getCtx(), tt.args.req)
			if err != nil && tt.isErr {
				return
			}

			if tt.isErr {
				if stream == nil {
					t.Error("StreamJobLogs() returned nil stream, want error")
					return
				}

				_, err = stream.Recv()
				if err == nil {
					t.Error("StreamJobLogs() error = nil, want error")
				}
				return
			}

			if err != nil {
				t.Errorf("StreamJobLogs() error = %v, want nil", err)
				return
			}

			if stream == nil {
				t.Errorf("StreamJobLogs() stream = nil, want stream")
				return
			}

			// Read logs from the stream
			var logCount int
			var streamClosed bool

			// Use a timeout to prevent test from hanging
			timeout := time.After(300 * time.Millisecond)
			for {
				select {
				case <-timeout:
					// Test timeout - exit the loop
					goto done
				default:
					log, err := stream.Recv()
					if err != nil {
						// Stream ended or error occurred
						streamClosed = true
						goto done
					}
					if log != nil {
						logCount++
						// Verify log structure
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
				}
			}

		done:
			// Verify expectations based on test case
			if tt.want.expectClosed && !streamClosed {
				t.Errorf("expected stream to be closed, but it's still open")
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

func TestListJobs(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.ListJobsRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.ListJobsRequest)
		res   *jobspb.ListJobsResponse
		isErr bool
	}{
		{
			name: "success: with no container id",
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
				req: &jobspb.ListJobsRequest{
					WorkflowId: "workflow_id",
					UserId:     "user_id",
					Cursor:     "",
				},
			},
			mock: func(_ *jobspb.ListJobsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListJobs(
					gomock.Any(),
					gomock.Any(),
				).Return(&jobsmodel.ListJobsResponse{
					Jobs: []*jobsmodel.JobByWorkflowIDResponse{
						{
							ID:          "job_id",
							WorkflowID:  "workflow_id",
							JobStatus:   "PENDING",
							ScheduledAt: time.Now(),
							StartedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: true,
							},
							CompletedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: true,
							},
							CreatedAt: time.Now(),
							UpdatedAt: time.Now(),
						},
					},
				}, nil)
			},
			res: &jobspb.ListJobsResponse{
				Jobs: []*jobspb.JobsResponse{
					{
						Id:          "job_id",
						WorkflowId:  "workflow_id",
						Status:      "PENDING",
						ScheduledAt: time.Now().Format(time.RFC3339Nano),
						StartedAt:   time.Now().Format(time.RFC3339Nano),
						CompletedAt: time.Now().Format(time.RFC3339Nano),
						CreatedAt:   time.Now().Format(time.RFC3339Nano),
						UpdatedAt:   time.Now().Format(time.RFC3339Nano),
					},
				},
				Cursor: "",
			},
			isErr: false,
		},
		{
			name: "success: with container id",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &jobspb.ListJobsRequest{
					WorkflowId: "workflow_id",
					UserId:     "user_id",
					Cursor:     "",
				},
			},
			mock: func(_ *jobspb.ListJobsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListJobs(
					gomock.Any(),
					gomock.Any(),
				).Return(&jobsmodel.ListJobsResponse{
					Jobs: []*jobsmodel.JobByWorkflowIDResponse{
						{
							ID:          "job_id",
							WorkflowID:  "workflow_id",
							JobStatus:   "RUNNING",
							ContainerID: sql.NullString{String: "container_id", Valid: true},
							ScheduledAt: time.Now(),
							StartedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: true,
							},
							CompletedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: true,
							},
							CreatedAt: time.Now(),
							UpdatedAt: time.Now(),
						},
					},
				}, nil)
			},
			res: &jobspb.ListJobsResponse{
				Jobs: []*jobspb.JobsResponse{
					{
						Id:          "job_id",
						WorkflowId:  "workflow_id",
						Status:      "RUNNING",
						ContainerId: "container_id",
						ScheduledAt: time.Now().Format(time.RFC3339Nano),
						StartedAt:   time.Now().Format(time.RFC3339Nano),
						CompletedAt: time.Now().Format(time.RFC3339Nano),
						CreatedAt:   time.Now().Format(time.RFC3339Nano),
						UpdatedAt:   time.Now().Format(time.RFC3339Nano),
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
				req: &jobspb.ListJobsRequest{
					WorkflowId: "workflow_id",
					UserId:     "user_id",
					Cursor:     "",
				},
			},
			mock: func(_ *jobspb.ListJobsRequest) {
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
				req: &jobspb.ListJobsRequest{
					WorkflowId: "",
					UserId:     "",
					Cursor:     "",
				},
			},
			mock: func(_ *jobspb.ListJobsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListJobs(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "workflow_id is required"))
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
				req: &jobspb.ListJobsRequest{
					WorkflowId: "workflow_id",
					UserId:     "user_id",
					Cursor:     "",
				},
			},
			mock:  func(_ *jobspb.ListJobsRequest) {},
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
				req: &jobspb.ListJobsRequest{
					WorkflowId: "workflow_id",
					UserId:     "user_id",
					Cursor:     "",
				},
			},
			mock: func(_ *jobspb.ListJobsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListJobs(
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
			_, err := client.ListJobs(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("ListJobs() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("ListJobs() error = nil, want error")
				}
				return
			}
		})
	}
}
