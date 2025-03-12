package jobs_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/model"
	"github.com/hitesh22rana/chronoverse/internal/service/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/service/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestCreateJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo)

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
			},
			mock: func(req *jobspb.CreateJobRequest) {
				repo.EXPECT().CreateJob(
					gomock.Any(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
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
			},
			mock: func(req *jobspb.CreateJobRequest) {
				repo.EXPECT().CreateJob(
					gomock.Any(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
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
			jobID, err := s.CreateJob(t.Context(), tt.req)
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
	s := jobs.New(validator.New(), repo)

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
				Interval: 1,
			},
			mock: func(req *jobspb.UpdateJobRequest) {
				repo.EXPECT().UpdateJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
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
				Interval: 1,
			},
			mock: func(req *jobspb.UpdateJobRequest) {
				repo.EXPECT().UpdateJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
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
				Interval: 1,
			},
			mock: func(req *jobspb.UpdateJobRequest) {
				repo.EXPECT().UpdateJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
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
				Interval: 1,
			},
			mock: func(req *jobspb.UpdateJobRequest) {
				repo.EXPECT().UpdateJob(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
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
			err := s.UpdateJob(t.Context(), tt.req)
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
	s := jobs.New(validator.New(), repo)

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
			_, err := s.GetJob(t.Context(), tt.req)
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
	s := jobs.New(validator.New(), repo)

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
			_, err := s.GetJobByID(t.Context(), tt.req)
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

func TestScheduleJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo)

	type want struct {
		scheduledJobID string
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.ScheduleJobRequest
		mock  func(req *jobspb.ScheduleJobRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.ScheduleJobRequest{
				JobId:       "job_id",
				UserId:      "user1",
				ScheduledAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
			},
			mock: func(req *jobspb.ScheduleJobRequest) {
				repo.EXPECT().ScheduleJob(
					gomock.Any(),
					req.GetJobId(),
					req.GetUserId(),
					req.GetScheduledAt(),
				).Return("scheduled_job_id", nil)
			},
			want: want{
				scheduledJobID: "scheduled_job_id",
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &jobspb.ScheduleJobRequest{
				JobId:       "",
				UserId:      "user1",
				ScheduledAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
			},
			mock:  func(_ *jobspb.ScheduleJobRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid scheduled at time",
			req: &jobspb.ScheduleJobRequest{
				JobId:       "job_id",
				UserId:      "user1",
				ScheduledAt: "invalid_time",
			},
			mock:  func(_ *jobspb.ScheduleJobRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.ScheduleJobRequest{
				JobId:       "invalid_job_id",
				UserId:      "user1",
				ScheduledAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
			},
			mock: func(req *jobspb.ScheduleJobRequest) {
				repo.EXPECT().ScheduleJob(
					gomock.Any(),
					req.GetJobId(),
					req.GetUserId(),
					req.GetScheduledAt(),
				).Return("", status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not owned by user",
			req: &jobspb.ScheduleJobRequest{
				JobId:       "job_id",
				UserId:      "invalid_user_id",
				ScheduledAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
			},
			mock: func(req *jobspb.ScheduleJobRequest) {
				repo.EXPECT().ScheduleJob(
					gomock.Any(),
					req.GetJobId(),
					req.GetUserId(),
					req.GetScheduledAt(),
				).Return("", status.Error(codes.NotFound, "job not found or not owned by user"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.ScheduleJobRequest{
				JobId:       "job_id",
				UserId:      "user1",
				ScheduledAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
			},
			mock: func(req *jobspb.ScheduleJobRequest) {
				repo.EXPECT().ScheduleJob(
					gomock.Any(),
					req.GetJobId(),
					req.GetUserId(),
					req.GetScheduledAt(),
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
			scheduledJobID, err := s.ScheduleJob(t.Context(), tt.req)
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

			if scheduledJobID != tt.want.scheduledJobID {
				t.Errorf("expected scheduledJobID: %s, got: %s", tt.want.scheduledJobID, scheduledJobID)
			}
		})
	}
}

func TestUpdateScheduledJobStatus(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo)

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.UpdateScheduledJobStatusRequest
		mock  func(req *jobspb.UpdateScheduledJobStatusRequest)
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.UpdateScheduledJobStatusRequest{
				Id:     "scheduled_job_id",
				Status: "COMPLETED",
			},
			mock: func(req *jobspb.UpdateScheduledJobStatusRequest) {
				repo.EXPECT().UpdateScheduledJobStatus(
					gomock.Any(),
					req.GetId(),
					req.GetStatus(),
				).Return(nil)
			},
			isErr: false,
		},
		{
			name: "error: missing scheduled job ID",
			req: &jobspb.UpdateScheduledJobStatusRequest{
				Id:     "",
				Status: "COMPLETED",
			},
			mock:  func(_ *jobspb.UpdateScheduledJobStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: missing status",
			req: &jobspb.UpdateScheduledJobStatusRequest{
				Id:     "scheduled_job_id",
				Status: "",
			},
			mock:  func(_ *jobspb.UpdateScheduledJobStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: invalid status",
			req: &jobspb.UpdateScheduledJobStatusRequest{
				Id:     "scheduled_job_id",
				Status: "INVALID",
			},
			mock:  func(_ *jobspb.UpdateScheduledJobStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: scheduled job not found",
			req: &jobspb.UpdateScheduledJobStatusRequest{
				Id:     "invalid_scheduled_job_id",
				Status: "COMPLETED",
			},
			mock: func(req *jobspb.UpdateScheduledJobStatusRequest) {
				repo.EXPECT().UpdateScheduledJobStatus(
					gomock.Any(),
					req.GetId(),
					req.GetStatus(),
				).Return(status.Error(codes.NotFound, "scheduled job not found"))
			},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.UpdateScheduledJobStatusRequest{
				Id:     "scheduled_job_id",
				Status: "COMPLETED",
			},
			mock: func(req *jobspb.UpdateScheduledJobStatusRequest) {
				repo.EXPECT().UpdateScheduledJobStatus(
					gomock.Any(),
					req.GetId(),
					req.GetStatus(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			err := s.UpdateScheduledJobStatus(t.Context(), tt.req)
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

func TestGetScheduledJobByID(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo)

	type want struct {
		*model.GetScheduledJobByIDResponse
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.GetScheduledJobByIDRequest
		mock  func(req *jobspb.GetScheduledJobByIDRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.GetScheduledJobByIDRequest{
				Id: "scheduled_job_id",
			},
			mock: func(req *jobspb.GetScheduledJobByIDRequest) {
				repo.EXPECT().GetScheduledJobByID(
					gomock.Any(),
					req.GetId(),
				).Return(&model.GetScheduledJobByIDResponse{
					JobID:       "job_id",
					UserID:      "user1",
					Status:      "PENDING",
					ScheduledAt: time.Now().Add(time.Minute),
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
				}, nil)
			},
			want: want{
				&model.GetScheduledJobByIDResponse{
					JobID:       "job_id",
					UserID:      "user1",
					Status:      "PENDING",
					ScheduledAt: time.Now().Add(time.Minute),
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
			isErr: false,
		},
		{
			name: "error: missing scheduled job ID",
			req: &jobspb.GetScheduledJobByIDRequest{
				Id: "",
			},
			mock:  func(_ *jobspb.GetScheduledJobByIDRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: scheduled job not found",
			req: &jobspb.GetScheduledJobByIDRequest{
				Id: "invalid_scheduled_job_id",
			},
			mock: func(req *jobspb.GetScheduledJobByIDRequest) {
				repo.EXPECT().GetScheduledJobByID(
					gomock.Any(),
					req.GetId(),
				).Return(nil, status.Error(codes.NotFound, "scheduled job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.GetScheduledJobByIDRequest{
				Id: "scheduled_job_id",
			},
			mock: func(req *jobspb.GetScheduledJobByIDRequest) {
				repo.EXPECT().GetScheduledJobByID(
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
			_, err := s.GetScheduledJobByID(t.Context(), tt.req)
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
	s := jobs.New(validator.New(), repo)

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
				Cursor: "",
			},
			mock: func(req *jobspb.ListJobsByUserIDRequest) {
				repo.EXPECT().ListJobsByUserID(
					gomock.Any(),
					req.GetUserId(),
					req.GetCursor(),
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
					Cursor: "",
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
					Cursor: "",
				},
			},
			isErr: false,
		},
		{
			name: "error: missing user ID",
			req: &jobspb.ListJobsByUserIDRequest{
				UserId: "",
				Cursor: "",
			},
			mock:  func(_ *jobspb.ListJobsByUserIDRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: user not found",
			req: &jobspb.ListJobsByUserIDRequest{
				UserId: "invalid_user_id",
				Cursor: "",
			},
			mock: func(req *jobspb.ListJobsByUserIDRequest) {
				repo.EXPECT().ListJobsByUserID(
					gomock.Any(),
					req.GetUserId(),
					req.GetCursor(),
				).Return(nil, status.Error(codes.NotFound, "user not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.ListJobsByUserIDRequest{
				UserId: "user1",
				Cursor: "",
			},
			mock: func(req *jobspb.ListJobsByUserIDRequest) {
				repo.EXPECT().ListJobsByUserID(
					gomock.Any(),
					req.GetUserId(),
					req.GetCursor(),
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
			_, err := s.ListJobsByUserID(t.Context(), tt.req)
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
	s := jobs.New(validator.New(), repo)

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
				Cursor: "",
			},
			mock: func(req *jobspb.ListScheduledJobsRequest) {
				repo.EXPECT().ListScheduledJobs(
					gomock.Any(),
					req.GetJobId(),
					req.GetUserId(),
					req.GetCursor(),
				).Return(&model.ListScheduledJobsResponse{
					ScheduledJobs: []*model.ScheduledJobByJobIDResponse{
						{
							ID:          "scheduled_job_id",
							Status:      "PENDING",
							ScheduledAt: time.Now().Add(time.Minute),
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
					Cursor: "",
				}, nil)
			},
			want: want{
				&model.ListScheduledJobsResponse{
					ScheduledJobs: []*model.ScheduledJobByJobIDResponse{
						{
							ID:          "scheduled_job_id",
							Status:      "PENDING",
							ScheduledAt: time.Now().Add(time.Minute),
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
					Cursor: "",
				},
			},
			isErr: false,
		},
		{
			name: "error: missing job ID",
			req: &jobspb.ListScheduledJobsRequest{
				JobId:  "",
				UserId: "user_id",
				Cursor: "",
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
				Cursor: "",
			},
			mock: func(req *jobspb.ListScheduledJobsRequest) {
				repo.EXPECT().ListScheduledJobs(
					gomock.Any(),
					req.GetJobId(),
					req.GetUserId(),
					req.GetCursor(),
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
				Cursor: "",
			},
			mock: func(req *jobspb.ListScheduledJobsRequest) {
				repo.EXPECT().ListScheduledJobs(
					gomock.Any(),
					req.GetJobId(),
					req.GetUserId(),
					req.GetCursor(),
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
			_, err := s.ListScheduledJobs(t.Context(), tt.req)
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
