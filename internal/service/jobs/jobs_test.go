package jobs_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/model"
	auth "github.com/hitesh22rana/chronoverse/internal/service/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/service/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestCreateJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type want struct {
		jobID string
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.CreateJobRequest
		mock  func(req *jobspb.CreateJobRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.CreateJobRequest{
				UserId:   "user1",
				Name:     "job1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:     "HEARTBEAT",
				Interval: 1,
				MaxRetry: 3,
			},
			mock: func(req *jobspb.CreateJobRequest) {
				repo.EXPECT().CreateJob(
					gomock.Any(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
					req.GetMaxRetry(),
				).Return("job_id", nil)
			},
			want: want{
				jobID: "job_id",
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &jobspb.CreateJobRequest{
				UserId:   "",
				Name:     "job1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:     "",
				Interval: 1,
				MaxRetry: 3,
			},
			mock:  func(_ *jobspb.CreateJobRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid payload",
			req: &jobspb.CreateJobRequest{
				UserId:   "user1",
				Name:     "job1",
				Payload:  `invalid json`,
				Kind:     "HEARTBEAT",
				Interval: 1,
				MaxRetry: 3,
			},
			mock:  func(_ *jobspb.CreateJobRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.CreateJobRequest{
				UserId:   "user1",
				Name:     "job1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:     "HEARTBEAT",
				Interval: 1,
				MaxRetry: 3,
			},
			mock: func(req *jobspb.CreateJobRequest) {
				repo.EXPECT().CreateJob(
					gomock.Any(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
					req.GetMaxRetry(),
				).Return("", status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			jobID, err := s.CreateJob(context.Background(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if jobID != tt.want.jobID {
				t.Errorf("expected jobID: %s, got: %s", tt.want.jobID, jobID)
			}
		})
	}
}

func TestUpdateJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type want struct {
		jobID string
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.UpdateJobRequest
		mock  func(req *jobspb.UpdateJobRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.UpdateJobRequest{
				Id:       "job_id",
				UserId:   "user1",
				Name:     "job1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:     "HEARTBEAT",
				Interval: 1,
			},
			mock: func(req *jobspb.UpdateJobRequest) {
				repo.EXPECT().UpdateJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
				).Return(nil)
			},
			want: want{
				jobID: "job_id",
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &jobspb.UpdateJobRequest{
				Id:       "",
				UserId:   "user1",
				Name:     "job1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:     "",
				Interval: 1,
			},
			mock:  func(_ *jobspb.UpdateJobRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid payload",
			req: &jobspb.UpdateJobRequest{
				Id:       "job_id",
				UserId:   "user1",
				Name:     "job1",
				Payload:  `invalid json`,
				Kind:     "HEARTBEAT",
				Interval: 1,
			},
			mock:  func(_ *jobspb.UpdateJobRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.UpdateJobRequest{
				Id:       "invalid_job_id",
				UserId:   "user1",
				Name:     "job1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:     "HEARTBEAT",
				Interval: 1,
			},
			mock: func(req *jobspb.UpdateJobRequest) {
				repo.EXPECT().UpdateJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
				).Return(status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not owned by user",
			req: &jobspb.UpdateJobRequest{
				Id:       "job_id",
				UserId:   "invalid_user_id",
				Name:     "job1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:     "HEARTBEAT",
				Interval: 1,
			},
			mock: func(req *jobspb.UpdateJobRequest) {
				repo.EXPECT().UpdateJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
				).Return(status.Error(codes.NotFound, "job not found or not owned by user"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.UpdateJobRequest{
				Id:       "job_id",
				UserId:   "user1",
				Name:     "job1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:     "HEARTBEAT",
				Interval: 1,
			},
			mock: func(req *jobspb.UpdateJobRequest) {
				repo.EXPECT().UpdateJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			err := s.UpdateJob(context.Background(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}

func TestGetJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type want struct {
		*model.GetJobResponse
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.GetJobRequest
		mock  func(req *jobspb.GetJobRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.GetJobRequest{
				Id:     "job_id",
				UserId: "user_id",
			},
			mock: func(req *jobspb.GetJobRequest) {
				repo.EXPECT().GetJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
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
			want: want{
				&model.GetJobResponse{
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
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &jobspb.GetJobRequest{
				Id:     "",
				UserId: "user_id",
			},
			mock:  func(_ *jobspb.GetJobRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid user",
			req: &jobspb.GetJobRequest{
				Id:     "job_id",
				UserId: "invalid_user_id",
			},
			mock: func(req *jobspb.GetJobRequest) {
				repo.EXPECT().GetJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.NotFound, "job not found or not owned by user"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.GetJobRequest{
				Id:     "invalid_job_id",
				UserId: "user_id",
			},
			mock: func(req *jobspb.GetJobRequest) {
				repo.EXPECT().GetJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.GetJobRequest{
				Id:     "job_id",
				UserId: "user_id",
			},
			mock: func(req *jobspb.GetJobRequest) {
				repo.EXPECT().GetJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.GetJob(context.Background(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}

func TestGetJobByID(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type want struct {
		*model.GetJobByIDResponse
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.GetJobByIDRequest
		mock  func(req *jobspb.GetJobByIDRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.GetJobByIDRequest{
				Id: "job_id",
			},
			mock: func(req *jobspb.GetJobByIDRequest) {
				repo.EXPECT().GetJobByID(
					gomock.Any(),
					req.GetId(),
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
			want: want{
				&model.GetJobByIDResponse{
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
				},
			},
			isErr: false,
		},
		{
			name: "error: missing job ID",
			req: &jobspb.GetJobByIDRequest{
				Id: "",
			},
			mock:  func(_ *jobspb.GetJobByIDRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.GetJobByIDRequest{
				Id: "invalid_job_id",
			},
			mock: func(req *jobspb.GetJobByIDRequest) {
				repo.EXPECT().GetJobByID(
					gomock.Any(),
					req.GetId(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.GetJobByIDRequest{
				Id: "job_id",
			},
			mock: func(req *jobspb.GetJobByIDRequest) {
				repo.EXPECT().GetJobByID(
					gomock.Any(),
					req.GetId(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.GetJobByID(context.Background(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}

func TestListJobsByUserID(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type want struct {
		*model.ListJobsByUserIDResponse
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.ListJobsByUserIDRequest
		mock  func(req *jobspb.ListJobsByUserIDRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.ListJobsByUserIDRequest{
				UserId: "user1",
			},
			mock: func(req *jobspb.ListJobsByUserIDRequest) {
				repo.EXPECT().ListJobsByUserID(
					gomock.Any(),
					req.GetUserId(),
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
				}, nil)
			},
			want: want{
				&model.ListJobsByUserIDResponse{
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
				},
			},
			isErr: false,
		},
		{
			name: "error: missing user ID",
			req: &jobspb.ListJobsByUserIDRequest{
				UserId: "",
			},
			mock:  func(_ *jobspb.ListJobsByUserIDRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: user not found",
			req: &jobspb.ListJobsByUserIDRequest{
				UserId: "invalid_user_id",
			},
			mock: func(req *jobspb.ListJobsByUserIDRequest) {
				repo.EXPECT().ListJobsByUserID(
					gomock.Any(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.NotFound, "user not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.ListJobsByUserIDRequest{
				UserId: "user1",
			},
			mock: func(req *jobspb.ListJobsByUserIDRequest) {
				repo.EXPECT().ListJobsByUserID(
					gomock.Any(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.ListJobsByUserID(context.Background(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}

func TestListScheduledJobs(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type want struct {
		*model.ListScheduledJobsResponse
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.ListScheduledJobsRequest
		mock  func(req *jobspb.ListScheduledJobsRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.ListScheduledJobsRequest{
				JobId:  "job_id",
				UserId: "user_id",
			},
			mock: func(req *jobspb.ListScheduledJobsRequest) {
				repo.EXPECT().ListScheduledJobs(
					gomock.Any(),
					req.GetJobId(),
					req.GetUserId(),
				).Return(&model.ListScheduledJobsResponse{
					ScheduledJobs: []*model.ScheduledJobByJobIDResponse{
						{
							ID:          "scheduled_job_id",
							Status:      "PENDING",
							ScheduledAt: time.Now().Add(time.Minute),
							RetryCount:  0,
							MaxRetry:    3,
							StartedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: false,
							},
							CompletedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: false,
							},
							CreatedAt: time.Now(),
							UpdatedAt: time.Now(),
						},
					},
				}, nil)
			},
			want: want{
				&model.ListScheduledJobsResponse{
					ScheduledJobs: []*model.ScheduledJobByJobIDResponse{
						{
							ID:          "scheduled_job_id",
							Status:      "PENDING",
							ScheduledAt: time.Now().Add(time.Minute),
							RetryCount:  0,
							MaxRetry:    3,
							StartedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: false,
							},
							CompletedAt: sql.NullTime{
								Time:  time.Now(),
								Valid: false,
							},
							CreatedAt: time.Now(),
							UpdatedAt: time.Now(),
						},
					},
				},
			},
			isErr: false,
		},
		{
			name: "error: missing job ID",
			req: &jobspb.ListScheduledJobsRequest{
				JobId:  "",
				UserId: "user_id",
			},
			mock:  func(_ *jobspb.ListScheduledJobsRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.ListScheduledJobsRequest{
				JobId:  "invalid_job_id",
				UserId: "user_id",
			},
			mock: func(req *jobspb.ListScheduledJobsRequest) {
				repo.EXPECT().ListScheduledJobs(
					gomock.Any(),
					req.GetJobId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.ListScheduledJobsRequest{
				JobId:  "job_id",
				UserId: "user_id",
			},
			mock: func(req *jobspb.ListScheduledJobsRequest) {
				repo.EXPECT().ListScheduledJobs(
					gomock.Any(),
					req.GetJobId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.ListScheduledJobs(context.Background(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}
