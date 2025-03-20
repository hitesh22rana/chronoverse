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
	"github.com/hitesh22rana/chronoverse/internal/model"
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

func TestCreateJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.CreateJobRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.CreateJobRequest)
		res   *jobspb.CreateJobResponse
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
				req: &jobspb.CreateJobRequest{
					UserId:   "user1",
					Name:     "job1",
					Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:     "HEARTBEAT",
					Interval: 1,
				},
			},
			mock: func(_ *jobspb.CreateJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().CreateJob(
					gomock.Any(),
					gomock.Any(),
				).Return("job_id", nil)
			},
			res: &jobspb.CreateJobResponse{
				Id: "job_id",
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
				req: &jobspb.CreateJobRequest{
					UserId:   "user1",
					Name:     "job1",
					Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:     "HEARTBEAT",
					Interval: 1,
				},
			},
			mock: func(_ *jobspb.CreateJobRequest) {
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
				req: &jobspb.CreateJobRequest{
					UserId:   "",
					Name:     "",
					Payload:  "",
					Kind:     "",
					Interval: 0,
				},
			},
			mock: func(_ *jobspb.CreateJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().CreateJob(
					gomock.Any(),
					gomock.Any(),
				).Return("", status.Error(codes.InvalidArgument, "user_id, name, payload, kind, interval, and max_retry are required"))
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
				req: &jobspb.CreateJobRequest{
					UserId:   "user1",
					Name:     "job1",
					Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:     "HEARTBEAT",
					Interval: 1,
				},
			},
			mock:  func(_ *jobspb.CreateJobRequest) {},
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
				req: &jobspb.CreateJobRequest{
					UserId:   "user1",
					Name:     "job1",
					Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:     "HEARTBEAT",
					Interval: 1,
				},
			},
			mock: func(_ *jobspb.CreateJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().CreateJob(
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
			_, err := client.CreateJob(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("CreateJob() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("CreateJob() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestUpdateJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.UpdateJobRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.UpdateJobRequest)
		res   *jobspb.UpdateJobResponse
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
				req: &jobspb.UpdateJobRequest{
					Id:       "job_id",
					UserId:   "user1",
					Name:     "job1",
					Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
					Interval: 1,
				},
			},
			mock: func(_ *jobspb.UpdateJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateJob(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &jobspb.UpdateJobResponse{},
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
				req: &jobspb.UpdateJobRequest{
					Id:       "job_id",
					UserId:   "user1",
					Name:     "job1",
					Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
					Interval: 1,
				},
			},
			mock: func(_ *jobspb.UpdateJobRequest) {
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
				req: &jobspb.UpdateJobRequest{
					Id:       "",
					UserId:   "",
					Name:     "",
					Payload:  "",
					Interval: 0,
				},
			},
			mock: func(_ *jobspb.UpdateJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateJob(
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
				req: &jobspb.UpdateJobRequest{
					Id:       "job_id",
					UserId:   "user1",
					Name:     "job1",
					Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
					Interval: 1,
				},
			},
			mock:  func(_ *jobspb.UpdateJobRequest) {},
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
				req: &jobspb.UpdateJobRequest{
					Id:       "job_id",
					UserId:   "user1",
					Name:     "job1",
					Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
					Interval: 1,
				},
			},
			mock: func(_ *jobspb.UpdateJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateJob(
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
			_, err := client.UpdateJob(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("UpdateJob() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("UpdateJob() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestUpdateJobBuildStatus(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.UpdateJobBuildStatusRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.UpdateJobBuildStatusRequest)
		res   *jobspb.UpdateJobBuildStatusResponse
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
				req: &jobspb.UpdateJobBuildStatusRequest{
					Id:          "job_id",
					BuildStatus: "COMPLETED",
				},
			},
			mock: func(_ *jobspb.UpdateJobBuildStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateJobBuildStatus(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &jobspb.UpdateJobBuildStatusResponse{},
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
				req: &jobspb.UpdateJobBuildStatusRequest{
					Id:          "job_id",
					BuildStatus: "COMPLETED",
				},
			},
			mock:  func(_ *jobspb.UpdateJobBuildStatusRequest) {},
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
				req: &jobspb.UpdateJobBuildStatusRequest{
					Id:          "job_id",
					BuildStatus: "COMPLETED",
				},
			},
			mock: func(_ *jobspb.UpdateJobBuildStatusRequest) {
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
				req: &jobspb.UpdateJobBuildStatusRequest{
					Id:          "",
					BuildStatus: "",
				},
			},
			mock: func(_ *jobspb.UpdateJobBuildStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateJobBuildStatus(
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
				req: &jobspb.UpdateJobBuildStatusRequest{
					Id:          "job_id",
					BuildStatus: "COMPLETED",
				},
			},
			mock:  func(_ *jobspb.UpdateJobBuildStatusRequest) {},
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
				req: &jobspb.UpdateJobBuildStatusRequest{
					Id:          "job_id",
					BuildStatus: "COMPLETED",
				},
			},
			mock: func(_ *jobspb.UpdateJobBuildStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateJobBuildStatus(
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
			_, err := client.UpdateJobBuildStatus(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("UpdateJobBuildStatus() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("UpdateJobBuildStatus() error = nil, want error")
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
					Id:     "job_id",
					UserId: "user1",
				},
			},
			mock: func(_ *jobspb.GetJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJob(
					gomock.Any(),
					gomock.Any(),
				).Return(&model.GetJobResponse{
					ID:             "job_id",
					Name:           "job1",
					Payload:        `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:           "HEARTBEAT",
					JobBuildStatus: "COMPLETED",
					Interval:       1,
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
					TerminatedAt: sql.NullTime{
						Time:  time.Now(),
						Valid: true,
					},
				}, nil)
			},
			res: &jobspb.GetJobResponse{
				Id:           "job_id",
				Name:         "job1",
				Payload:      `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:         "HEARTBEAT",
				BuildStatus:  "COMPLETED",
				Interval:     1,
				CreatedAt:    time.Now().Format(time.RFC3339Nano),
				UpdatedAt:    time.Now().Format(time.RFC3339Nano),
				TerminatedAt: time.Now().Format(time.RFC3339Nano),
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
				req: &jobspb.GetJobRequest{
					Id:     "job_id",
					UserId: "user1",
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
					Id:     "",
					UserId: "",
				},
			},
			mock: func(_ *jobspb.GetJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetJob(
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
				req: &jobspb.GetJobRequest{
					Id:     "job_id",
					UserId: "user1",
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
					Id:     "job_id",
					UserId: "user1",
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
				).Return(&model.GetJobByIDResponse{
					UserID:         "user1",
					Name:           "job1",
					Payload:        `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:           "HEARTBEAT",
					JobBuildStatus: "COMPLETED",
					Interval:       1,
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
					TerminatedAt: sql.NullTime{
						Time:  time.Now(),
						Valid: true,
					},
				}, nil)
			},
			res: &jobspb.GetJobByIDResponse{
				UserId:       "user1",
				Name:         "job1",
				Payload:      `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:         "HEARTBEAT",
				BuildStatus:  "COMPLETED",
				Interval:     1,
				CreatedAt:    time.Now().Format(time.RFC3339Nano),
				UpdatedAt:    time.Now().Format(time.RFC3339Nano),
				TerminatedAt: time.Now().Format(time.RFC3339Nano),
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

func TestTerminateJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.TerminateJobRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.TerminateJobRequest)
		res   *jobspb.TerminateJobResponse
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
				req: &jobspb.TerminateJobRequest{
					Id:     "job_id",
					UserId: "user1",
				},
			},
			mock: func(_ *jobspb.TerminateJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().TerminateJob(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &jobspb.TerminateJobResponse{},
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
				req: &jobspb.TerminateJobRequest{
					Id:     "job_id",
					UserId: "user1",
				},
			},
			mock: func(_ *jobspb.TerminateJobRequest) {
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
				req: &jobspb.TerminateJobRequest{
					Id:     "",
					UserId: "",
				},
			},
			mock: func(_ *jobspb.TerminateJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().TerminateJob(
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
				req: &jobspb.TerminateJobRequest{
					Id:     "job_id",
					UserId: "user1",
				},
			},
			mock:  func(_ *jobspb.TerminateJobRequest) {},
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
				req: &jobspb.TerminateJobRequest{
					Id:     "job_id",
					UserId: "user1",
				},
			},
			mock: func(_ *jobspb.TerminateJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().TerminateJob(
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
			_, err := client.TerminateJob(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("TerminateJob() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("TerminateJob() error = nil, want error")
				}
				return
			}
		})
	}
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
					JobId:       "job_id",
					UserId:      "user1",
					ScheduledAt: time.Now().Format(time.RFC3339Nano),
				},
			},
			mock: func(_ *jobspb.ScheduleJobRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ScheduleJob(
					gomock.Any(),
					gomock.Any(),
				).Return("scheduled_job_id", nil)
			},
			res: &jobspb.ScheduleJobResponse{
				Id: "scheduled_job_id",
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
					JobId:       "job_id",
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
					JobId:       "job_id",
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
					JobId:       "",
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
					JobId:       "job_id",
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
					JobId:       "job_id",
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

func TestUpdateScheduledJobStatus(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.UpdateScheduledJobStatusRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.UpdateScheduledJobStatusRequest)
		res   *jobspb.UpdateScheduledJobStatusResponse
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
				req: &jobspb.UpdateScheduledJobStatusRequest{
					Id:     "scheduled_job_id",
					Status: "QUEUED",
				},
			},
			mock: func(_ *jobspb.UpdateScheduledJobStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateScheduledJobStatus(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &jobspb.UpdateScheduledJobStatusResponse{},
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
				req: &jobspb.UpdateScheduledJobStatusRequest{
					Id:     "scheduled_job_id",
					Status: "QUEUED",
				},
			},
			mock:  func(_ *jobspb.UpdateScheduledJobStatusRequest) {},
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
				req: &jobspb.UpdateScheduledJobStatusRequest{
					Id:     "scheduled_job_id",
					Status: "QUEUED",
				},
			},
			mock: func(_ *jobspb.UpdateScheduledJobStatusRequest) {
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
				req: &jobspb.UpdateScheduledJobStatusRequest{
					Id:     "",
					Status: "",
				},
			},
			mock: func(_ *jobspb.UpdateScheduledJobStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateScheduledJobStatus(
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
				req: &jobspb.UpdateScheduledJobStatusRequest{
					Id:     "scheduled_job_id",
					Status: "QUEUED",
				},
			},
			mock:  func(_ *jobspb.UpdateScheduledJobStatusRequest) {},
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
				req: &jobspb.UpdateScheduledJobStatusRequest{
					Id:     "scheduled_job_id",
					Status: "QUEUED",
				},
			},
			mock: func(_ *jobspb.UpdateScheduledJobStatusRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateScheduledJobStatus(
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
			_, err := client.UpdateScheduledJobStatus(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("UpdateScheduledJobStatus() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("UpdateScheduledJobStatus() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestGetScheduledJobByID(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.GetScheduledJobByIDRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.GetScheduledJobByIDRequest)
		res   *jobspb.GetScheduledJobByIDResponse
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
				req: &jobspb.GetScheduledJobByIDRequest{
					Id: "job_id",
				},
			},
			mock: func(_ *jobspb.GetScheduledJobByIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetScheduledJobByID(
					gomock.Any(),
					gomock.Any(),
				).Return(&model.GetScheduledJobByIDResponse{
					ID:                 "scheduled_job_id",
					JobID:              "job_id",
					UserID:             "user1",
					ScheduledJobStatus: "QUEUED",
					ScheduledAt:        time.Now(),
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
			res: &jobspb.GetScheduledJobByIDResponse{
				Id:          "scheduled_job_id",
				JobId:       "job_id",
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
				req: &jobspb.GetScheduledJobByIDRequest{
					Id: "job_id",
				},
			},
			mock:  func(_ *jobspb.GetScheduledJobByIDRequest) {},
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
				req: &jobspb.GetScheduledJobByIDRequest{
					Id: "job_id",
				},
			},
			mock: func(_ *jobspb.GetScheduledJobByIDRequest) {
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
				req: &jobspb.GetScheduledJobByIDRequest{
					Id: "",
				},
			},
			mock: func(_ *jobspb.GetScheduledJobByIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetScheduledJobByID(
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
				req: &jobspb.GetScheduledJobByIDRequest{
					Id: "job_id",
				},
			},
			mock:  func(_ *jobspb.GetScheduledJobByIDRequest) {},
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
				req: &jobspb.GetScheduledJobByIDRequest{
					Id: "job_id",
				},
			},
			mock: func(_ *jobspb.GetScheduledJobByIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetScheduledJobByID(
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
			_, err := client.GetScheduledJobByID(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("GetScheduledJobByID() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("GetScheduledJobByID() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestListJobsByUserID(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.ListJobsByUserIDRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.ListJobsByUserIDRequest)
		res   *jobspb.ListJobsByUserIDResponse
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
				req: &jobspb.ListJobsByUserIDRequest{
					UserId: "user1",
					Cursor: "",
				},
			},
			mock: func(_ *jobspb.ListJobsByUserIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListJobsByUserID(
					gomock.Any(),
					gomock.Any(),
				).Return(&model.ListJobsByUserIDResponse{
					Jobs: []*model.JobByUserIDResponse{
						{
							ID:             "job_id",
							Name:           "job1",
							Payload:        `{"action": "run", "params": {"foo": "bar"}}`,
							Kind:           "HEARTBEAT",
							JobBuildStatus: "COMPLETED",
							Interval:       1,
							CreatedAt:      time.Now(),
							UpdatedAt:      time.Now(),
							TerminatedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: true,
							},
						},
					},
					Cursor: "",
				}, nil)
			},
			res: &jobspb.ListJobsByUserIDResponse{
				Jobs: []*jobspb.JobsByUserIDResponse{
					{
						Id:           "job_id",
						Name:         "job1",
						Payload:      `{"action": "run", "params": {"foo": "bar"}}`,
						Kind:         "HEARTBEAT",
						BuildStatus:  "COMPLETED",
						Interval:     1,
						CreatedAt:    time.Now().Format(time.RFC3339Nano),
						UpdatedAt:    time.Now().Format(time.RFC3339Nano),
						TerminatedAt: time.Now().Format(time.RFC3339Nano),
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
				req: &jobspb.ListJobsByUserIDRequest{
					UserId: "user1",
					Cursor: "",
				},
			},
			mock: func(_ *jobspb.ListJobsByUserIDRequest) {
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
				req: &jobspb.ListJobsByUserIDRequest{
					UserId: "",
					Cursor: "",
				},
			},
			mock: func(_ *jobspb.ListJobsByUserIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListJobsByUserID(
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
				req: &jobspb.ListJobsByUserIDRequest{
					UserId: "user1",
					Cursor: "",
				},
			},
			mock:  func(_ *jobspb.ListJobsByUserIDRequest) {},
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
				req: &jobspb.ListJobsByUserIDRequest{
					UserId: "user1",
					Cursor: "",
				},
			},
			mock: func(_ *jobspb.ListJobsByUserIDRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListJobsByUserID(
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
			_, err := client.ListJobsByUserID(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("ListJobsByUserID() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("ListJobsByUserID() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestListScheduledJobs(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(jobs.New(t.Context(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *jobspb.ListScheduledJobsRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*jobspb.ListScheduledJobsRequest)
		res   *jobspb.ListScheduledJobsResponse
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
				req: &jobspb.ListScheduledJobsRequest{
					JobId:  "job_id",
					UserId: "user_id",
					Cursor: "",
				},
			},
			mock: func(_ *jobspb.ListScheduledJobsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListScheduledJobs(
					gomock.Any(),
					gomock.Any(),
				).Return(&model.ListScheduledJobsResponse{
					ScheduledJobs: []*model.ScheduledJobByJobIDResponse{
						{
							ID:                 "scheduled_job_id",
							ScheduledJobStatus: "PENDING",
							ScheduledAt:        time.Now(),
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
			res: &jobspb.ListScheduledJobsResponse{
				ScheduledJobs: []*jobspb.ScheduledJobsResponse{
					{
						Id:          "scheduled_job_id",
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
				req: &jobspb.ListScheduledJobsRequest{
					JobId:  "job_id",
					UserId: "user_id",
					Cursor: "",
				},
			},
			mock: func(_ *jobspb.ListScheduledJobsRequest) {
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
				req: &jobspb.ListScheduledJobsRequest{
					JobId:  "",
					UserId: "user_id",
					Cursor: "",
				},
			},
			mock: func(_ *jobspb.ListScheduledJobsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListScheduledJobs(
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
				req: &jobspb.ListScheduledJobsRequest{
					JobId:  "job_id",
					UserId: "user_id",
					Cursor: "",
				},
			},
			mock:  func(_ *jobspb.ListScheduledJobsRequest) {},
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
				req: &jobspb.ListScheduledJobsRequest{
					JobId:  "job_id",
					UserId: "user_id",
					Cursor: "",
				},
			},
			mock: func(_ *jobspb.ListScheduledJobsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListScheduledJobs(
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
			_, err := client.ListScheduledJobs(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("ListScheduledJobs() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("ListScheduledJobs() error = nil, want error")
				}
				return
			}
		})
	}
}
