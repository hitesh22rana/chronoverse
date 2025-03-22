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
