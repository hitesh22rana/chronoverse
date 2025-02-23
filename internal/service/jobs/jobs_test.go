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

func TestListScheduledJobsByJobID(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type want struct {
		*model.ListScheduledJobsByJobIDResponse
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.ListScheduledJobsByJobIDRequest
		mock  func(req *jobspb.ListScheduledJobsByJobIDRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.ListScheduledJobsByJobIDRequest{
				JobId: "job_id",
			},
			mock: func(req *jobspb.ListScheduledJobsByJobIDRequest) {
				repo.EXPECT().ListScheduledJobsByJobID(
					gomock.Any(),
					req.GetJobId(),
				).Return(&model.ListScheduledJobsByJobIDResponse{
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
				&model.ListScheduledJobsByJobIDResponse{
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
			req: &jobspb.ListScheduledJobsByJobIDRequest{
				JobId: "",
			},
			mock:  func(_ *jobspb.ListScheduledJobsByJobIDRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.ListScheduledJobsByJobIDRequest{
				JobId: "invalid_job_id",
			},
			mock: func(req *jobspb.ListScheduledJobsByJobIDRequest) {
				repo.EXPECT().ListScheduledJobsByJobID(
					gomock.Any(),
					req.GetJobId(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.ListScheduledJobsByJobIDRequest{
				JobId: "job_id",
			},
			mock: func(req *jobspb.ListScheduledJobsByJobIDRequest) {
				repo.EXPECT().ListScheduledJobsByJobID(
					gomock.Any(),
					req.GetJobId(),
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
			_, err := s.ListScheduledJobsByJobID(context.Background(), tt.req)
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
