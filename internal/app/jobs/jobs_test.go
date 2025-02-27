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

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	"github.com/hitesh22rana/chronoverse/internal/app/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/app/jobs/mock"
	"github.com/hitesh22rana/chronoverse/internal/model"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)

	server := jobs.New(context.Background(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, svc)

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

	client, _close := initClient(jobs.New(context.Background(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, svc))
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
								context.Background(), "users-test",
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
					MaxRetry: 3,
				},
			},
			mock: func(_ *jobspb.CreateJobRequest) {
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
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								context.Background(), "users-test",
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
					MaxRetry: 0,
				},
			},
			mock: func(_ *jobspb.CreateJobRequest) {
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
						context.Background(),
					)
				},
				req: &jobspb.CreateJobRequest{
					UserId:   "user1",
					Name:     "job1",
					Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:     "HEARTBEAT",
					Interval: 1,
					MaxRetry: 3,
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
								context.Background(), "users-test",
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
					MaxRetry: 3,
				},
			},
			mock: func(_ *jobspb.CreateJobRequest) {
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

	client, _close := initClient(jobs.New(context.Background(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, svc))
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
								context.Background(), "users-test",
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
					Kind:     "HEARTBEAT",
					Interval: 1,
				},
			},
			mock: func(_ *jobspb.UpdateJobRequest) {
				svc.EXPECT().UpdateJob(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &jobspb.UpdateJobResponse{},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								context.Background(), "users-test",
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
					Kind:     "",
					Interval: 0,
				},
			},
			mock: func(_ *jobspb.UpdateJobRequest) {
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
						context.Background(),
					)
				},
				req: &jobspb.UpdateJobRequest{
					Id:       "job_id",
					UserId:   "user1",
					Name:     "job1",
					Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:     "HEARTBEAT",
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
								context.Background(), "users-test",
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
					Kind:     "HEARTBEAT",
					Interval: 1,
				},
			},
			mock: func(_ *jobspb.UpdateJobRequest) {
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

func TestGetJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)

	client, _close := initClient(jobs.New(context.Background(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, svc))
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
								context.Background(), "users-test",
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
				svc.EXPECT().GetJob(
					gomock.Any(),
					gomock.Any(),
				).Return(&model.GetJobResponse{
					ID:        "job_id",
					Name:      "job1",
					Payload:   `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:      "HEARTBEAT",
					Interval:  1,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
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
				Interval:     1,
				CreatedAt:    time.Now().Format(time.RFC3339),
				UpdatedAt:    time.Now().Format(time.RFC3339),
				TerminatedAt: time.Now().Format(time.RFC3339),
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								context.Background(), "users-test",
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
						context.Background(),
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
								context.Background(), "users-test",
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

	client, _close := initClient(jobs.New(context.Background(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, svc))
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
								context.Background(), "users-test",
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
			mock: func(_ *jobspb.GetJobByIDRequest) {
				svc.EXPECT().GetJobByID(
					gomock.Any(),
					gomock.Any(),
				).Return(&model.GetJobByIDResponse{
					UserID:    "user1",
					Name:      "job1",
					Payload:   `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:      "HEARTBEAT",
					Interval:  1,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
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
				Interval:     1,
				CreatedAt:    time.Now().Format(time.RFC3339),
				UpdatedAt:    time.Now().Format(time.RFC3339),
				TerminatedAt: time.Now().Format(time.RFC3339),
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								context.Background(), "users-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &jobspb.GetJobByIDRequest{
					Id: "",
				},
			},
			mock: func(_ *jobspb.GetJobByIDRequest) {
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
						context.Background(),
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
								context.Background(), "users-test",
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
			mock: func(_ *jobspb.GetJobByIDRequest) {
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

func TestListJobsByUserID(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := jobsmock.NewMockService(ctrl)

	client, _close := initClient(jobs.New(context.Background(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, svc))
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
								context.Background(), "users-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &jobspb.ListJobsByUserIDRequest{
					UserId:        "user1",
					NextPageToken: "",
				},
			},
			mock: func(_ *jobspb.ListJobsByUserIDRequest) {
				svc.EXPECT().ListJobsByUserID(
					gomock.Any(),
					gomock.Any(),
				).Return(&model.ListJobsByUserIDResponse{
					Jobs: []*model.JobByUserIDResponse{
						{
							ID:        "job_id",
							Name:      "job1",
							Payload:   `{"action": "run", "params": {"foo": "bar"}}`,
							Kind:      "HEARTBEAT",
							Interval:  1,
							CreatedAt: time.Now(),
							UpdatedAt: time.Now(),
							TerminatedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: true,
							},
						},
					},
					NextPageToken: "",
				}, nil)
			},
			res: &jobspb.ListJobsByUserIDResponse{
				Jobs: []*jobspb.JobsByUserIDResponse{
					{
						Id:           "job_id",
						Name:         "job1",
						Payload:      `{"action": "run", "params": {"foo": "bar"}}`,
						Kind:         "HEARTBEAT",
						Interval:     1,
						CreatedAt:    time.Now().Format(time.RFC3339),
						UpdatedAt:    time.Now().Format(time.RFC3339),
						TerminatedAt: time.Now().Format(time.RFC3339),
					},
				},
				NextPageToken: "",
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								context.Background(), "users-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &jobspb.ListJobsByUserIDRequest{
					UserId:        "",
					NextPageToken: "",
				},
			},
			mock: func(_ *jobspb.ListJobsByUserIDRequest) {
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
						context.Background(),
					)
				},
				req: &jobspb.ListJobsByUserIDRequest{
					UserId:        "user1",
					NextPageToken: "",
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
								context.Background(), "users-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &jobspb.ListJobsByUserIDRequest{
					UserId:        "user1",
					NextPageToken: "",
				},
			},
			mock: func(_ *jobspb.ListJobsByUserIDRequest) {
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

	client, _close := initClient(jobs.New(context.Background(), &jobs.Config{
		Deadline: 500 * time.Millisecond,
	}, svc))
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
								context.Background(), "users-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &jobspb.ListScheduledJobsRequest{
					JobId:         "job_id",
					UserId:        "user_id",
					NextPageToken: "",
				},
			},
			mock: func(_ *jobspb.ListScheduledJobsRequest) {
				svc.EXPECT().ListScheduledJobs(
					gomock.Any(),
					gomock.Any(),
				).Return(&model.ListScheduledJobsResponse{
					ScheduledJobs: []*model.ScheduledJobByJobIDResponse{
						{
							ID:          "scheduled_job_id",
							Status:      "PENDING",
							ScheduledAt: time.Now(),
							RetryCount:  0,
							MaxRetry:    3,
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
						ScheduledAt: time.Now().Format(time.RFC3339),
						RetryCount:  0,
						MaxRetry:    3,
						StartedAt:   time.Now().Format(time.RFC3339),
						CompletedAt: time.Now().Format(time.RFC3339),
						CreatedAt:   time.Now().Format(time.RFC3339),
						UpdatedAt:   time.Now().Format(time.RFC3339),
					},
				},
				NextPageToken: "",
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								context.Background(), "users-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &jobspb.ListScheduledJobsRequest{
					JobId:         "",
					UserId:        "user_id",
					NextPageToken: "",
				},
			},
			mock: func(_ *jobspb.ListScheduledJobsRequest) {
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
						context.Background(),
					)
				},
				req: &jobspb.ListScheduledJobsRequest{
					JobId:         "job_id",
					UserId:        "user_id",
					NextPageToken: "",
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
								context.Background(), "users-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &jobspb.ListScheduledJobsRequest{
					JobId:         "job_id",
					UserId:        "user_id",
					NextPageToken: "",
				},
			},
			mock: func(_ *jobspb.ListScheduledJobsRequest) {
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
